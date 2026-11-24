// Package daemon orchestrates drive detection, synchronization and the state
// that the D-Bus API exposes.
package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"

	"walky/internal/config"
	"walky/internal/device"
	"walky/internal/engine"
	"walky/internal/setup"
	"walky/internal/state"
	"walky/internal/store"
	"walky/internal/tray"
)

const maxEvents = 200

// Sync states reported to clients.
const (
	StateNoDevice = "no_device"
	StateIdle     = "idle"
	StateSyncing  = "syncing"
	StatePaused   = "paused"
	StateError    = "error"
)

var (
	// ErrNoDevice means no Walky drive is attached.
	ErrNoDevice = errors.New("no walky drive is attached")
	// ErrBusy means a sync is already running.
	ErrBusy = errors.New("a sync is already running")
)

// Runner performs one snapshot.
type Runner interface {
	Run(ctx context.Context, opt engine.Options, progress func(engine.Progress)) (engine.Result, error)
}

// Watcher reports attach and detach events.
type Watcher interface {
	Run(ctx context.Context, out chan<- device.Event) error
}

// Ejector unmounts and powers off a drive.
type Ejector interface {
	Eject(ctx context.Context, mountPoint string) error
}

// Mounter mounts labelled filesystems that are not mounted yet.
type Mounter interface {
	MountLabeled(ctx context.Context, label string) (int, error)
}

type mounterFunc func(context.Context, string) (int, error)

func (f mounterFunc) MountLabeled(ctx context.Context, label string) (int, error) {
	return f(ctx, label)
}

// Emitter receives state changes for the D-Bus service.
type Emitter interface {
	StateChanged(state string)
	Progress(done, total int, bytes int64)
	FileDone(path, op, result string)
}

// Config is the daemon's static configuration.
type Config struct {
	RepoPath      string
	RepoID        string
	Excludes      []string
	MaxFileSize   int64
	Workers       int
	IndexPath     string
	Machine       string
	AutoSync      bool
	StoreOverride string
	Interval      time.Duration
	Mode          string
	Conflict      string
	BasePath      string
	TrashDir      string
	ConfigPath    string
	Retention     RetentionSettings
	AutoMount     bool
	DeviceLabel   string
	AutoGC        bool
	GCInterval    time.Duration
	NoTray        bool
}

// RetentionSettings mirrors the [retention] section for the UI.
type RetentionSettings struct {
	Daily       int
	Weekly      int
	Monthly     int
	TrashDays   int
	MachineDays int
}

// Settings is the editable subset of the configuration.
type Settings struct {
	Excludes  []string
	Retention RetentionSettings
	Conflict  string
}

// Event is one recent activity entry exposed to clients.
type Event struct {
	Path   string
	Op     string
	Result string
	Size   int64
	Time   int64
}

// Status is a snapshot of daemon state.
type Status struct {
	State         string
	Device        string
	StoreRoot     string
	MountPoint    string
	RepoID        string
	LastSync      int64
	Generation    int64
	Pending       int
	Conflicts     int
	FreeBytes     int64
	Paused        bool
	ProgressDone  int
	ProgressTotal int
	ProgressBytes int64
	LastError     string
}

type runnerFunc func(context.Context, engine.Options, func(engine.Progress)) (engine.Result, error)

func (f runnerFunc) Run(ctx context.Context, opt engine.Options, progress func(engine.Progress)) (engine.Result, error) {
	return f(ctx, opt, progress)
}

// Daemon owns the device state and the sync lifecycle.
type Daemon struct {
	cfg     Config
	watcher Watcher
	runner  Runner
	ejector Ejector
	log     *slog.Logger

	sleepWatch  func(context.Context) (<-chan bool, error)
	checkFS     func(string) error
	mounter     Mounter
	mounting    bool
	mounts      func() ([]device.Mount, error)
	mountFilter func(string) bool
	scanStores  func() ([]device.Device, error)
	gcRunning   bool
	trayItem    *tray.Tray
	shutdown    context.CancelFunc

	mu       sync.Mutex
	emitter  Emitter
	state    string
	dev      *device.Device
	lastSync time.Time
	gen      int64
	events   []Event
	paused   bool
	syncing  bool
	cancel   context.CancelFunc
	done     chan struct{}
	lastErr  string
	progress engine.Progress
	settings Settings
}

// New creates a daemon with production dependencies.
func New(cfg Config, log *slog.Logger) *Daemon {
	if log == nil {
		log = slog.Default()
	}
	if cfg.Interval <= 0 {
		cfg.Interval = 2 * time.Second
	}
	if cfg.DeviceLabel == "" {
		cfg.DeviceLabel = device.DefaultLabel
	}
	if cfg.GCInterval <= 0 {
		cfg.GCInterval = 24 * time.Hour
	}
	return &Daemon{
		cfg:        cfg,
		watcher:    device.NewWatcher(cfg.Interval),
		runner:     runnerFunc(engine.Sync),
		ejector:    ejectorFunc(device.Eject),
		log:        log,
		sleepWatch: device.WatchSleep,
		checkFS:    store.CheckFilesystem,
		mounter:    mounterFunc(device.MountLabeled),
		mounts:     func() ([]device.Mount, error) { return device.ReadMounts("/proc/self/mountinfo") },
		scanStores: func() ([]device.Device, error) { return device.NewWatcher(0).Scan() },
		state:      StateNoDevice,
		settings: Settings{
			Excludes:  append([]string(nil), cfg.Excludes...),
			Retention: cfg.Retention,
			Conflict:  cfg.Conflict,
		},
	}
}

type ejectorFunc func(context.Context, string) error

func (f ejectorFunc) Eject(ctx context.Context, mountPoint string) error {
	return f(ctx, mountPoint)
}

// Settings returns the editable configuration.
func (d *Daemon) Settings() Settings {
	d.mu.Lock()
	defer d.mu.Unlock()
	return Settings{
		Excludes:  append([]string(nil), d.settings.Excludes...),
		Retention: d.settings.Retention,
		Conflict:  d.settings.Conflict,
	}
}

// UpdateSettings validates, persists and applies new settings. A change of the
// repository path is rejected because it would invalidate the local base.
func (d *Daemon) UpdateSettings(s Settings) error {
	if d.cfg.ConfigPath == "" {
		return errors.New("config path is not set")
	}
	cfg, err := config.Load(d.cfg.ConfigPath)
	if err != nil {
		return err
	}
	cfg.Exclude.Patterns = append([]string(nil), s.Excludes...)
	cfg.Retention.Daily = s.Retention.Daily
	cfg.Retention.Weekly = s.Retention.Weekly
	cfg.Retention.Monthly = s.Retention.Monthly
	cfg.Retention.TrashDays = s.Retention.TrashDays
	cfg.Sync.Conflict = config.ConflictPolicy(s.Conflict)
	if err := cfg.Validate(); err != nil {
		return err
	}
	if err := d.applyConfig(cfg); err != nil {
		return err
	}
	return config.Save(d.cfg.ConfigPath, cfg)
}

// ReloadConfig re-reads the configuration file and applies it.
func (d *Daemon) ReloadConfig() error {
	if d.cfg.ConfigPath == "" {
		return errors.New("config path is not set")
	}
	cfg, err := config.Load(d.cfg.ConfigPath)
	if err != nil {
		return err
	}
	return d.applyConfig(cfg)
}

func (d *Daemon) applyConfig(cfg config.Config) error {
	repo, err := cfg.ExpandedRepoPath()
	if err != nil {
		return err
	}
	if d.cfg.RepoPath != "" && repo != d.cfg.RepoPath {
		d.mu.Lock()
		busy := d.syncing || d.dev != nil
		d.mu.Unlock()
		if busy {
			return errors.New("detach the drive and wait for the sync to finish before changing repo.path")
		}
	}
	maxSize, err := cfg.Sync.MaxFileSizeBytes()
	if err != nil {
		return err
	}
	idxPath, err := state.IndexPath(repo)
	if err != nil {
		return err
	}
	basePath, err := state.BasePath(repo)
	if err != nil {
		return err
	}
	trashDir, err := state.TrashDir()
	if err != nil {
		return err
	}

	d.mu.Lock()
	d.cfg.RepoPath = repo
	d.cfg.RepoID = cfg.Repo.RepoID
	d.cfg.Excludes = append([]string(nil), cfg.Exclude.Patterns...)
	d.cfg.MaxFileSize = maxSize
	d.cfg.Workers = cfg.Sync.Workers
	d.cfg.IndexPath = idxPath
	d.cfg.BasePath = basePath
	d.cfg.TrashDir = trashDir
	d.cfg.Mode = string(cfg.Sync.Mode)
	d.cfg.Conflict = string(cfg.Sync.Conflict)
	d.cfg.AutoSync = cfg.Device.AutoSync
	d.cfg.AutoMount = cfg.Device.AutoMount
	d.cfg.DeviceLabel = cfg.Device.Label
	if d.cfg.DeviceLabel == "" {
		d.cfg.DeviceLabel = device.DefaultLabel
	}
	d.cfg.AutoGC = cfg.Maintenance.AutoGC
	if cfg.Maintenance.GCIntervalHours > 0 {
		d.cfg.GCInterval = time.Duration(cfg.Maintenance.GCIntervalHours) * time.Hour
	}
	d.cfg.Retention = RetentionSettings{
		Daily: cfg.Retention.Daily, Weekly: cfg.Retention.Weekly,
		Monthly: cfg.Retention.Monthly, TrashDays: cfg.Retention.TrashDays,
		MachineDays: cfg.Retention.MachineDays,
	}
	d.settings = Settings{
		Excludes:  append([]string(nil), cfg.Exclude.Patterns...),
		Retention: d.cfg.Retention,
		Conflict:  string(cfg.Sync.Conflict),
	}
	d.mu.Unlock()
	return nil
}

// SetEmitter registers the D-Bus signal emitter. Call before Run.
func (d *Daemon) SetEmitter(e Emitter) {
	d.mu.Lock()
	d.emitter = e
	d.mu.Unlock()
}

// Run processes device events until ctx is cancelled.
func (d *Daemon) Run(ctx context.Context) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	d.mu.Lock()
	d.shutdown = cancel
	d.mu.Unlock()
	d.startTray(runCtx)
	ctx = runCtx

	if d.cfg.StoreOverride != "" {
		dev, err := d.overrideDevice()
		if err != nil {
			return err
		}
		d.attach(dev)
		<-ctx.Done()
		d.stopSync()
		return nil
	}

	go d.automountLoop(ctx)
	go d.gcLoop(ctx)

	events := make(chan device.Event, 8)
	go func() {
		if err := d.watcher.Run(ctx, events); err != nil && ctx.Err() == nil {
			d.log.Error("device watcher stopped", "err", err)
		}
	}()

	var sleepCh <-chan bool
	if d.sleepWatch != nil {
		var err error
		sleepCh, err = d.sleepWatch(ctx)
		if err != nil {
			d.log.Warn("sleep monitoring disabled", "err", err)
		}
	}

	for {
		select {
		case <-ctx.Done():
			d.stopSync()
			return nil
		case ev := <-events:
			switch ev.Kind {
			case device.Attached:
				d.attach(ev.Device)
			case device.Detached:
				d.detach(ev.Device)
			}
		case sleeping, ok := <-sleepCh:
			if !ok {
				sleepCh = nil
				continue
			}
			if sleeping {
				d.log.Info("system is suspending, stopping sync")
				d.stopSync()
			} else {
				d.log.Info("system resumed")
				d.tryAutoSync()
			}
		}
	}
}

func (d *Daemon) startTray(ctx context.Context) {
	if d.cfg.NoTray {
		return
	}
	t, err := tray.New(tray.Actions{
		Snapshot: func() tray.Snapshot {
			st := d.Status()
			return tray.Snapshot{
				State: st.State, Device: st.Device,
				Generation: st.Generation, Paused: st.Paused,
			}
		},
		SyncNow: func() { _ = d.SyncNow(false) },
		TogglePause: func() bool {
			st := d.Status()
			d.Pause(!st.Paused)
			return !st.Paused
		},
		OpenUI: tray.OpenUI,
		Verify: func() {
			go func() {
				if _, err := d.Verify(false); err != nil {
					d.log.Warn("tray verify failed", "err", err)
				}
			}()
		},
		Quit: d.quit,
	})
	if err != nil {
		d.log.Debug("tray unavailable", "err", err)
		return
	}
	d.mu.Lock()
	d.trayItem = t
	d.mu.Unlock()
	go t.Run(ctx)
}

// reattach looks for an already mounted store with the configured repo_id.
// Changing the config (adopting a drive) does not change the mount table, so
// the polling watcher would not emit a new attach event by itself.
func (d *Daemon) reattach() {
	if d.scanStores == nil {
		return
	}
	stores, err := d.scanStores()
	if err != nil {
		d.log.Debug("reattach scan failed", "err", err)
		return
	}
	d.mu.Lock()
	repoID := d.cfg.RepoID
	current := ""
	if d.dev != nil {
		current = d.dev.StoreRoot
	}
	d.mu.Unlock()
	for _, dev := range stores {
		if dev.Marker.RepoID != repoID || dev.StoreRoot == current {
			continue
		}
		d.attach(dev)
		return
	}
}

func (d *Daemon) quit() {
	d.log.Info("quit requested from the tray")
	// "Quit Walky" should leave neither a stray daemon nor a window that
	// reports a missing daemon.
	_ = exec.Command("pkill", "-x", "walky-ui").Run()
	d.mu.Lock()
	cancel := d.shutdown
	d.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (d *Daemon) automountLoop(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	d.tryAutomount(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.tryAutomount(ctx)
		}
	}
}

func (d *Daemon) tryAutomount(ctx context.Context) {
	d.mu.Lock()
	ready := d.cfg.AutoMount && d.cfg.StoreOverride == "" && !d.mounting && d.mounter != nil
	label := d.cfg.DeviceLabel
	if ready {
		d.mounting = true
	}
	d.mu.Unlock()
	if !ready {
		return
	}
	defer func() {
		d.mu.Lock()
		d.mounting = false
		d.mu.Unlock()
	}()

	mountCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	n, err := d.mounter.MountLabeled(mountCtx, label)
	if err != nil {
		d.log.Debug("automount failed", "err", err)
		return
	}
	if n > 0 {
		d.log.Info("mounted candidate drives", "count", n)
	}
}

func (d *Daemon) gcLoop(ctx context.Context) {
	interval := d.cfg.GCInterval
	if interval <= 0 {
		interval = 24 * time.Hour
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.tryGC(ctx)
		}
	}
}

func (d *Daemon) tryGC(ctx context.Context) {
	d.mu.Lock()
	ready := d.cfg.AutoGC && d.dev != nil && !d.syncing && !d.gcRunning && d.cfg.StoreOverride == ""
	dev := d.dev
	cfg := d.cfg
	if ready {
		d.gcRunning = true
	}
	d.mu.Unlock()
	if !ready || dev == nil {
		return
	}
	defer func() {
		d.mu.Lock()
		d.gcRunning = false
		d.mu.Unlock()
	}()

	st, err := store.Open(dev.StoreRoot)
	if err != nil {
		d.log.Warn("scheduled gc: cannot open store", "err", err)
		return
	}
	rep, err := st.GC(store.GCOptions{
		KeepDaily:         cfg.Retention.Daily,
		KeepWeekly:        cfg.Retention.Weekly,
		KeepMonthly:       cfg.Retention.Monthly,
		TrashDays:         cfg.Retention.TrashDays,
		MachineMaxAgeDays: cfg.Retention.MachineDays,
		KeepMachine:       cfg.Machine,
	})
	if err != nil {
		d.log.Warn("scheduled gc failed", "err", err)
		d.recordEvent(Event{Path: dev.StoreRoot, Op: "gc", Result: "error", Time: time.Now().Unix()})
		return
	}
	d.log.Info("scheduled gc finished", "freed", rep.FreedBytes, "packs", rep.RemovedPacks)
	d.recordEvent(Event{Path: dev.StoreRoot, Op: "gc", Result: "ok", Size: rep.FreedBytes, Time: time.Now().Unix()})
}

func (d *Daemon) overrideDevice() (device.Device, error) {
	marker, err := store.ReadMarker(d.cfg.StoreOverride)
	if err != nil {
		return device.Device{}, err
	}
	abs := d.cfg.StoreOverride
	label := marker.Label
	if label == "" {
		label = filepath.Base(abs)
	}
	return device.Device{
		MountPoint: abs,
		StoreRoot:  abs,
		Label:      label,
		Marker:     marker,
	}, nil
}

func (d *Daemon) attach(dev device.Device) {
	if d.checkFS != nil {
		if err := d.checkFS(dev.StoreRoot); err != nil {
			d.log.Error("refusing drive", "root", dev.StoreRoot, "err", err)
			return
		}
	}
	if overlaps(d.cfg.RepoPath, dev.StoreRoot) || overlaps(dev.StoreRoot, d.cfg.RepoPath) {
		d.log.Error("refusing drive because it overlaps the sync folder",
			"repo", d.cfg.RepoPath, "store", dev.StoreRoot)
		return
	}
	d.mu.Lock()
	if d.dev != nil && d.dev.StoreRoot == dev.StoreRoot {
		d.mu.Unlock()
		return
	}
	if d.cfg.RepoID != "" && dev.Marker.RepoID != d.cfg.RepoID {
		d.mu.Unlock()
		d.log.Warn("ignoring drive with unexpected repo_id",
			"expected", d.cfg.RepoID, "actual", dev.Marker.RepoID, "root", dev.StoreRoot)
		return
	}
	d.dev = &dev
	d.lastErr = ""
	if d.paused {
		d.state = StatePaused
	} else {
		d.state = StateIdle
	}
	auto := d.cfg.AutoSync && !d.paused
	d.mu.Unlock()

	d.log.Info("drive attached", "root", dev.StoreRoot, "label", dev.Label)
	d.recordEvent(Event{Path: dev.StoreRoot, Op: "attach", Result: "ok", Time: time.Now().Unix()})
	d.emitState()
	if auto {
		if err := d.startSync(); err != nil && !errors.Is(err, ErrBusy) {
			d.log.Warn("auto sync not started", "err", err)
		}
	}
}

func (d *Daemon) detach(dev device.Device) {
	d.mu.Lock()
	if d.dev == nil || d.dev.Key() != dev.Key() {
		d.mu.Unlock()
		return
	}
	cancel := d.cancel
	d.dev = nil
	if cancel != nil {
		cancel()
	}
	if d.paused {
		d.state = StatePaused
	} else {
		d.state = StateNoDevice
	}
	d.mu.Unlock()

	d.log.Info("drive detached", "root", dev.StoreRoot)
	d.recordEvent(Event{Path: dev.StoreRoot, Op: "detach", Result: "ok", Time: time.Now().Unix()})
	d.emitState()
}

func (d *Daemon) stopSync() {
	d.mu.Lock()
	cancel := d.cancel
	d.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (d *Daemon) tryAutoSync() {
	d.mu.Lock()
	ready := d.cfg.AutoSync && !d.paused && d.dev != nil && !d.syncing
	d.mu.Unlock()
	if ready {
		if err := d.startSync(); err != nil && !errors.Is(err, ErrBusy) {
			d.log.Warn("auto sync not started", "err", err)
		}
	}
}

// SyncNow starts a sync and optionally waits for it to finish.
func (d *Daemon) SyncNow(wait bool) error {
	if err := d.startSync(); err != nil {
		return err
	}
	if !wait {
		return nil
	}
	d.mu.Lock()
	done := d.done
	d.mu.Unlock()
	if done != nil {
		<-done
	}
	d.mu.Lock()
	lastErr := d.lastErr
	d.mu.Unlock()
	if lastErr != "" {
		return errors.New(lastErr)
	}
	return nil
}

func (d *Daemon) startSync() error {
	d.mu.Lock()
	if d.dev == nil {
		d.mu.Unlock()
		return ErrNoDevice
	}
	if d.syncing {
		d.mu.Unlock()
		return ErrBusy
	}
	ctx, cancel := context.WithCancel(context.Background())
	d.cancel = cancel
	d.done = make(chan struct{})
	d.syncing = true
	d.state = StateSyncing
	d.lastErr = ""
	d.progress = engine.Progress{}
	dev := *d.dev
	cfg := d.cfg
	d.mu.Unlock()

	d.emitState()
	d.log.Info("sync started", "root", dev.StoreRoot)
	go func() {
		defer close(d.done)
		res, err := d.runner.Run(ctx, engine.Options{
			StoreRoot:   dev.StoreRoot,
			RepoPath:    cfg.RepoPath,
			Excludes:    cfg.Excludes,
			MaxFileSize: cfg.MaxFileSize,
			Workers:     cfg.Workers,
			IndexPath:   cfg.IndexPath,
			Machine:     cfg.Machine,
			Mode:        cfg.Mode,
			Conflict:    cfg.Conflict,
			BasePath:    cfg.BasePath,
			TrashDir:    cfg.TrashDir,
		}, d.onProgress)
		d.finishSync(ctx, dev, res, err)
	}()
	return nil
}

func (d *Daemon) finishSync(ctx context.Context, dev device.Device, res engine.Result, err error) {
	cancelled := ctx.Err() != nil

	d.mu.Lock()
	d.syncing = false
	d.cancel = nil
	d.progress = engine.Progress{}
	switch {
	case cancelled:
		if d.dev == nil {
			d.state = StateNoDevice
		} else if d.paused {
			d.state = StatePaused
		} else {
			d.state = StateIdle
		}
	case err != nil:
		d.state = StateError
		d.lastErr = err.Error()
	default:
		d.state = StateIdle
		d.gen = res.Gen
		d.lastSync = time.Now()
	}
	d.mu.Unlock()

	switch {
	case cancelled:
		d.log.Info("sync aborted", "root", dev.StoreRoot)
		d.recordEvent(Event{Path: dev.StoreRoot, Op: "sync", Result: "aborted", Time: time.Now().Unix()})
	case err != nil:
		d.log.Error("sync failed", "root", dev.StoreRoot, "err", err)
		d.recordEvent(Event{Path: dev.StoreRoot, Op: "sync", Result: "error", Time: time.Now().Unix()})
	default:
		d.log.Info("sync finished", "root", dev.StoreRoot, "generation", res.Gen, "changed", res.Changed)
		d.recordEvent(Event{Path: dev.StoreRoot, Op: "sync", Result: "ok", Size: res.UploadBytes, Time: time.Now().Unix()})
	}
	d.emitState()
}

func (d *Daemon) onProgress(p engine.Progress) {
	d.mu.Lock()
	d.progress = p
	em := d.emitter
	d.mu.Unlock()
	if em != nil {
		em.Progress(p.Done, p.Total, p.Bytes)
	}
	if p.Path != "" && p.Op != "" {
		d.recordEvent(Event{Path: p.Path, Op: p.Op, Result: "ok", Size: p.Size, Time: time.Now().Unix()})
	}
}

func (d *Daemon) recordEvent(e Event) {
	d.mu.Lock()
	d.events = append(d.events, e)
	if len(d.events) > maxEvents {
		d.events = d.events[len(d.events)-maxEvents:]
	}
	em := d.emitter
	d.mu.Unlock()
	switch e.Op {
	case "push", "pull", "delete", "conflict":
		if em != nil {
			em.FileDone(e.Path, e.Op, e.Result)
		}
	}
}

func (d *Daemon) emitState() {
	d.mu.Lock()
	state := d.state
	em := d.emitter
	d.mu.Unlock()
	if em != nil {
		em.StateChanged(state)
	}
}

// Status returns a consistent snapshot of the daemon state.
func (d *Daemon) Status() Status {
	d.mu.Lock()
	defer d.mu.Unlock()

	st := Status{
		State:         d.state,
		LastSync:      unixTime(d.lastSync),
		Generation:    d.gen,
		Paused:        d.paused,
		ProgressDone:  d.progress.Done,
		ProgressTotal: d.progress.Total,
		ProgressBytes: d.progress.Bytes,
		LastError:     d.lastErr,
	}
	if d.dev != nil {
		st.Device = d.dev.Label
		st.StoreRoot = d.dev.StoreRoot
		st.MountPoint = d.dev.MountPoint
		st.RepoID = d.dev.Marker.RepoID
		st.FreeBytes = freeBytes(d.dev.StoreRoot)
	}
	return st
}

// Events returns the most recent events, newest first.
func (d *Daemon) Events(limit int) []Event {
	if limit <= 0 {
		limit = 10
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if limit > len(d.events) {
		limit = len(d.events)
	}
	out := make([]Event, 0, limit)
	for i := len(d.events) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, d.events[i])
	}
	return out
}

// Pause stops the current sync and blocks auto-sync until resumed.
func (d *Daemon) Pause(paused bool) {
	d.mu.Lock()
	d.paused = paused
	cancel := d.cancel
	if paused {
		if d.dev != nil {
			d.state = StatePaused
		}
	} else if d.dev != nil && !d.syncing {
		d.state = StateIdle
	}
	d.mu.Unlock()

	if paused && cancel != nil {
		cancel()
	}
	d.emitState()
	if !paused {
		d.tryAutoSync()
	}
}

// Drive is a removable ext4 mount offered to the setup UI.
type Drive struct {
	MountPoint  string
	Label       string
	StoreRoot   string
	RepoID      string
	Initialized bool
	FreeBytes   int64
	Encrypted   bool
}

// Drives lists removable ext4 mounts under /run/media, /media and /mnt.
func (d *Daemon) Drives() ([]Drive, error) {
	readMounts := d.mounts
	if readMounts == nil {
		readMounts = func() ([]device.Mount, error) {
			return device.ReadMounts("/proc/self/mountinfo")
		}
	}
	mounts, err := readMounts()
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool)
	var out []Drive
	for _, m := range mounts {
		filter := d.mountFilter
		if filter == nil {
			filter = isRemovableMount
		}
		if !filter(m.Point) || seen[m.Point] {
			continue
		}
		checkFS := d.checkFS
		if checkFS == nil {
			checkFS = store.CheckFilesystem
		}
		if err := checkFS(m.Point); err != nil {
			continue
		}
		seen[m.Point] = true
		drive := Drive{
			MountPoint: m.Point,
			Label:      filepath.Base(m.Point),
			StoreRoot:  filepath.Join(m.Point, "WALKY"),
			FreeBytes:  freeBytes(m.Point),
		}
		if marker, err := store.ReadMarker(drive.StoreRoot); err == nil {
			drive.Initialized = true
			drive.RepoID = marker.RepoID
			if marker.Label != "" {
				drive.Label = marker.Label
			}
		} else if marker, err := store.ReadMarker(m.Point); err == nil {
			drive.StoreRoot = m.Point
			drive.Initialized = true
			drive.RepoID = marker.RepoID
			if marker.Label != "" {
				drive.Label = marker.Label
			}
		}
		drive.Encrypted, _ = device.EncryptionStatus(m.Point)
		out = append(out, drive)
	}
	return out, nil
}

func isRemovableMount(point string) bool {
	for _, prefix := range []string{"/run/media/", "/media/", "/mnt/"} {
		if strings.HasPrefix(point, prefix) {
			return true
		}
	}
	return false
}

// InitStore creates a new store on a listed drive and points the config at the
// repository. The GUI and CLI share setup.Initialize.
func (d *Daemon) InitStore(mountPoint, repoPath string) (string, error) {
	drives, err := d.Drives()
	if err != nil {
		return "", err
	}
	var target *Drive
	for i := range drives {
		if drives[i].MountPoint == mountPoint {
			target = &drives[i]
			break
		}
	}
	if target == nil {
		return "", errors.New("this mount is not a removable ext4 filesystem")
	}
	if target.Initialized {
		return "", fmt.Errorf("this drive already contains a store at %s", target.StoreRoot)
	}
	if d.cfg.ConfigPath == "" {
		return "", errors.New("config path is not set")
	}
	storeRoot := filepath.Join(mountPoint, "WALKY")
	checkFS := d.checkFS
	if checkFS == nil {
		checkFS = store.CheckFilesystem
	}
	res, err := setup.Initialize(storeRoot, false, checkFS)
	if err != nil {
		return "", err
	}
	if err := setup.ConfigureRepo(d.cfg.ConfigPath, repoPath, res.RepoID); err != nil {
		return res.RepoID, err
	}
	if err := d.ReloadConfig(); err != nil {
		return res.RepoID, err
	}
	d.reattach()
	d.recordEvent(Event{Path: storeRoot, Op: "init", Result: "ok", Time: time.Now().Unix()})
	d.log.Info("store initialized", "root", storeRoot, "repo", repoPath)
	return res.RepoID, nil
}

// UseStore binds an already initialized drive to a repository.
func (d *Daemon) UseStore(mountPoint, repoPath string) (string, error) {
	drives, err := d.Drives()
	if err != nil {
		return "", err
	}
	for _, drive := range drives {
		if drive.MountPoint != mountPoint {
			continue
		}
		if !drive.Initialized {
			return "", errors.New("this drive has no Walky store yet")
		}
		if d.cfg.ConfigPath == "" {
			return "", errors.New("config path is not set")
		}
		if err := setup.ConfigureRepo(d.cfg.ConfigPath, repoPath, drive.RepoID); err != nil {
			return "", err
		}
		if err := d.ReloadConfig(); err != nil {
			return drive.RepoID, err
		}
		d.reattach()
		d.recordEvent(Event{Path: drive.StoreRoot, Op: "adopt", Result: "ok", Time: time.Now().Unix()})
		return drive.RepoID, nil
	}
	return "", errors.New("mount is not a removable ext4 filesystem")
}

// Eject unmounts the current drive. It refuses while a sync is running.
func (d *Daemon) Eject(ctx context.Context) error {
	d.mu.Lock()
	dev := d.dev
	syncing := d.syncing
	d.mu.Unlock()
	if dev == nil {
		return ErrNoDevice
	}
	if syncing {
		return ErrBusy
	}
	if err := d.ejector.Eject(ctx, dev.MountPoint); err != nil {
		return err
	}
	d.detach(*dev)
	return nil
}

// Verify checks the current store and returns a human-readable summary.
func (d *Daemon) Verify(full bool) (string, error) {
	d.mu.Lock()
	dev := d.dev
	d.mu.Unlock()
	if dev == nil {
		return "", ErrNoDevice
	}
	st, err := store.Open(dev.StoreRoot)
	if err != nil {
		return "", err
	}
	rep, err := st.Verify(full)
	if err != nil {
		return "", err
	}
	summary := fmt.Sprintf("generation %d: %d objects, %d entries",
		rep.Gen, rep.Objects, rep.Entries)
	if rep.OK() {
		return summary + ": ok", nil
	}
	return summary, fmt.Errorf("integrity check failed: %d missing, %d corrupt, %d size mismatches",
		len(rep.Missing), len(rep.Corrupt), len(rep.SizeMismatch))
}

func unixTime(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

func overlaps(a, b string) bool {
	rel, err := filepath.Rel(a, b)
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel))
}

func freeBytes(path string) int64 {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0
	}
	return int64(st.Bavail) * int64(st.Bsize)
}
