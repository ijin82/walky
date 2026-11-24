package daemon

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"walky/internal/config"
	"walky/internal/device"
	"walky/internal/engine"
	"walky/internal/store"
)

type fakeWatcher struct {
	events chan device.Event
}

func (w *fakeWatcher) Run(ctx context.Context, out chan<- device.Event) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case ev := <-w.events:
			select {
			case out <- ev:
			case <-ctx.Done():
				return nil
			}
		}
	}
}

type fakeRunner struct {
	mu      sync.Mutex
	calls   int
	opts    engine.Options
	started chan struct{}
	ctxDone chan struct{}
	block   bool
	gen     int64
	err     error
}

func (r *fakeRunner) Run(ctx context.Context, opt engine.Options, progress func(engine.Progress)) (engine.Result, error) {
	r.mu.Lock()
	r.calls++
	r.opts = opt
	r.mu.Unlock()
	if r.started != nil {
		select {
		case r.started <- struct{}{}:
		default:
		}
	}
	if progress != nil {
		progress(engine.Progress{Phase: "upload", Done: 1, Total: 1, Path: "a.txt", Size: 3, Op: "push"})
	}
	if r.block {
		<-ctx.Done()
		if r.ctxDone != nil {
			close(r.ctxDone)
		}
		return engine.Result{}, ctx.Err()
	}
	if r.err != nil {
		return engine.Result{}, r.err
	}
	return engine.Result{Gen: r.gen, Changed: true, Entries: 3}, nil
}

func (r *fakeRunner) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

type fakeEmitter struct {
	mu     sync.Mutex
	states []string
	events []string
}

func (e *fakeEmitter) StateChanged(state string) {
	e.mu.Lock()
	e.states = append(e.states, state)
	e.mu.Unlock()
}

func (e *fakeEmitter) Progress(int, int, int64) {}

func (e *fakeEmitter) FileDone(path, op, result string) {
	e.mu.Lock()
	e.events = append(e.events, path+" "+op+" "+result)
	e.mu.Unlock()
}

func (e *fakeEmitter) hasState(state string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, s := range e.states {
		if s == state {
			return true
		}
	}
	return false
}

func testDaemon(t *testing.T, runner Runner, auto bool) (*Daemon, *fakeWatcher) {
	t.Helper()
	w := &fakeWatcher{events: make(chan device.Event, 8)}
	d := &Daemon{
		cfg:     Config{RepoPath: t.TempDir(), AutoSync: auto, Machine: "test", NoTray: true},
		watcher: w,
		runner:  runner,
		ejector: ejectorFunc(func(context.Context, string) error { return nil }),
		log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		state:   StateNoDevice,
		checkFS: func(string) error { return nil },
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = d.Run(ctx)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("daemon did not stop")
		}
	})
	return d, w
}

func testDevice(t *testing.T) device.Device {
	t.Helper()
	return device.Device{
		MountPoint: t.TempDir(),
		StoreRoot:  t.TempDir(),
		Label:      "test",
		Marker:     store.Marker{Format: store.FormatVersion, RepoID: strings.Repeat("a", 32)},
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestAttachStartsSync(t *testing.T) {
	runner := &fakeRunner{started: make(chan struct{}, 1), gen: 5}
	d, w := testDaemon(t, runner, true)
	d.cfg.Mode = engine.ModeTwoWay
	em := &fakeEmitter{}
	d.SetEmitter(em)

	dev := testDevice(t)
	w.events <- device.Event{Kind: device.Attached, Device: dev}
	select {
	case <-runner.started:
	case <-time.After(2 * time.Second):
		t.Fatal("sync did not start")
	}
	waitFor(t, "idle state", func() bool { return d.Status().State == StateIdle })

	st := d.Status()
	if st.Generation != 5 || st.StoreRoot != dev.StoreRoot {
		t.Fatalf("status = %+v", st)
	}
	if runner.callCount() != 1 {
		t.Fatalf("runner calls = %d", runner.callCount())
	}
	runner.mu.Lock()
	mode := runner.opts.Mode
	runner.mu.Unlock()
	if mode != engine.ModeTwoWay {
		t.Fatalf("runner mode = %q, want two-way", mode)
	}
	if !em.hasState(StateSyncing) || !em.hasState(StateIdle) {
		t.Fatalf("emitted states = %v", em.states)
	}
}

func TestDetachCancelsSync(t *testing.T) {
	runner := &fakeRunner{
		block:   true,
		started: make(chan struct{}, 1),
		ctxDone: make(chan struct{}),
	}
	d, w := testDaemon(t, runner, true)
	dev := testDevice(t)
	w.events <- device.Event{Kind: device.Attached, Device: dev}
	select {
	case <-runner.started:
	case <-time.After(2 * time.Second):
		t.Fatal("sync did not start")
	}
	w.events <- device.Event{Kind: device.Detached, Device: dev}
	select {
	case <-runner.ctxDone:
	case <-time.After(2 * time.Second):
		t.Fatal("runner context was not cancelled")
	}
	waitFor(t, "no_device state", func() bool { return d.Status().State == StateNoDevice })
}

func TestPauseBlocksAutoSync(t *testing.T) {
	runner := &fakeRunner{started: make(chan struct{}, 1), gen: 1}
	d, w := testDaemon(t, runner, true)
	d.Pause(true)
	w.events <- device.Event{Kind: device.Attached, Device: testDevice(t)}
	time.Sleep(100 * time.Millisecond)
	if runner.callCount() != 0 {
		t.Fatalf("sync started while paused")
	}
	if d.Status().State != StatePaused {
		t.Fatalf("state = %q, want paused", d.Status().State)
	}
	d.Pause(false)
	select {
	case <-runner.started:
	case <-time.After(2 * time.Second):
		t.Fatal("sync did not start after resume")
	}
}

func TestSyncNowWithoutDevice(t *testing.T) {
	d, _ := testDaemon(t, &fakeRunner{}, true)
	if err := d.SyncNow(false); err != ErrNoDevice {
		t.Fatalf("SyncNow = %v, want ErrNoDevice", err)
	}
}

func TestEventsRecorded(t *testing.T) {
	runner := &fakeRunner{started: make(chan struct{}, 1), gen: 2}
	d, w := testDaemon(t, runner, true)
	w.events <- device.Event{Kind: device.Attached, Device: testDevice(t)}
	select {
	case <-runner.started:
	case <-time.After(2 * time.Second):
		t.Fatal("sync did not start")
	}
	waitFor(t, "sync event", func() bool {
		for _, ev := range d.Events(10) {
			if ev.Op == "sync" && ev.Result == "ok" {
				return true
			}
		}
		return false
	})
	ops := map[string]bool{}
	for _, ev := range d.Events(10) {
		ops[ev.Op] = true
	}
	for _, want := range []string{"attach", "push", "sync"} {
		if !ops[want] {
			t.Fatalf("missing %q event in %v", want, ops)
		}
	}
}

func TestRepoIDMismatchNeedsRecovery(t *testing.T) {
	runner := &fakeRunner{started: make(chan struct{}, 1)}
	d, w := testDaemon(t, runner, true)
	d.cfg.RepoID = strings.Repeat("b", 32)
	w.events <- device.Event{Kind: device.Attached, Device: testDevice(t)}
	time.Sleep(100 * time.Millisecond)
	if runner.callCount() != 0 {
		t.Fatal("foreign drive was synced")
	}
	st := d.Status()
	if st.Device == "" {
		t.Fatal("foreign drive is not shown")
	}
	if st.State != StateNeedsRecovery {
		t.Fatalf("state = %q, want %q", st.State, StateNeedsRecovery)
	}
}

func TestEjectWithoutDevice(t *testing.T) {
	d, _ := testDaemon(t, &fakeRunner{}, true)
	if err := d.Eject(context.Background()); err != ErrNoDevice {
		t.Fatalf("Eject = %v, want ErrNoDevice", err)
	}
}

func TestSyncNowReportsFailure(t *testing.T) {
	runner := &fakeRunner{started: make(chan struct{}, 1), err: errTest}
	d, w := testDaemon(t, runner, false)
	w.events <- device.Event{Kind: device.Attached, Device: testDevice(t)}
	waitFor(t, "attached device", func() bool { return d.Status().Device != "" })
	if err := d.SyncNow(true); err == nil {
		t.Fatal("SyncNow succeeded despite a runner error")
	}
	if d.Status().State != StateError {
		t.Fatalf("state = %q, want error", d.Status().State)
	}
}

func TestOverlappingPathsRejected(t *testing.T) {
	runner := &fakeRunner{started: make(chan struct{}, 1)}
	d, w := testDaemon(t, runner, true)
	dir := t.TempDir()
	d.cfg.RepoPath = dir
	dev := testDevice(t)
	dev.StoreRoot = dir + "/store"
	dev.MountPoint = dev.StoreRoot
	if err := os.MkdirAll(dev.StoreRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	w.events <- device.Event{Kind: device.Attached, Device: dev}
	time.Sleep(100 * time.Millisecond)
	if runner.callCount() != 0 {
		t.Fatal("overlapping store was synced")
	}
}

var errTest = errors.New("runner failed")

func TestFilesystemRejected(t *testing.T) {
	runner := &fakeRunner{started: make(chan struct{}, 1)}
	d, w := testDaemon(t, runner, true)
	d.checkFS = func(string) error { return errors.New("not ext4") }
	w.events <- device.Event{Kind: device.Attached, Device: testDevice(t)}
	time.Sleep(100 * time.Millisecond)
	if runner.callCount() != 0 {
		t.Fatal("store on an unsupported filesystem was synced")
	}
	if d.Status().Device != "" {
		t.Fatal("unsupported store was accepted")
	}
}

type fakeMounter struct {
	mu    sync.Mutex
	calls int
	label string
}

func (m *fakeMounter) MountLabeled(ctx context.Context, label string) (int, error) {
	m.mu.Lock()
	m.calls++
	m.label = label
	m.mu.Unlock()
	return 1, nil
}

func TestAutomountCallsMounter(t *testing.T) {
	d := New(Config{
		RepoPath:    t.TempDir(),
		AutoMount:   true,
		DeviceLabel: "WALKY",
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	m := &fakeMounter{}
	d.mounter = m
	d.tryAutomount(context.Background())
	if m.calls != 1 || m.label != "WALKY" {
		t.Fatalf("mounter calls = %d, label = %q", m.calls, m.label)
	}

	d.cfg.AutoMount = false
	d.tryAutomount(context.Background())
	if m.calls != 1 {
		t.Fatalf("mounter called with automount disabled")
	}
}

func TestScheduledGCRuns(t *testing.T) {
	root := t.TempDir()
	st, err := store.Init(root, false)
	if err != nil {
		t.Fatal(err)
	}
	hash, size, err := st.PutObject(strings.NewReader("alpha"))
	if err != nil {
		t.Fatal(err)
	}
	m := &store.Manifest{Entries: []store.Entry{{
		Path: "a.txt", Type: store.TypeFile, Size: size, Mode: 0o644, Hash: hash,
	}}}
	if _, err := st.Commit(m, store.CommitOptions{}); err != nil {
		t.Fatal(err)
	}

	d := New(Config{
		RepoPath: t.TempDir(), Machine: "current",
		AutoGC: true, GCInterval: time.Hour,
		Retention: RetentionSettings{Daily: 1, TrashDays: 30, MachineDays: 180},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	d.dev = &device.Device{StoreRoot: root, MountPoint: root, Label: "test"}

	d.tryGC(context.Background())
	found := false
	for _, ev := range d.Events(10) {
		if ev.Op == "gc" && ev.Result == "ok" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no gc event: %+v", d.Events(10))
	}
}

func TestDrivesAndInitStore(t *testing.T) {
	mount := t.TempDir()
	repo := t.TempDir()
	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	cfg := config.Default()
	cfg.Repo.Path = repo
	cfg.Device.AutoSync = false
	if err := config.Save(cfgPath, cfg); err != nil {
		t.Fatal(err)
	}
	d := New(Config{RepoPath: repo, ConfigPath: cfgPath, NoTray: true},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	d.mounts = func() ([]device.Mount, error) {
		return []device.Mount{{Point: mount, FSType: "ext4"}}, nil
	}
	d.checkFS = func(string) error { return nil }
	d.mountFilter = func(string) bool { return true }

	drives, err := d.Drives()
	if err != nil {
		t.Fatal(err)
	}
	if len(drives) != 1 || drives[0].Initialized {
		t.Fatalf("drives = %+v", drives)
	}

	id, err := d.InitStore(mount, repo)
	if err != nil {
		t.Fatalf("InitStore: %v", err)
	}
	if _, err := os.Stat(filepath.Join(mount, "WALKY", store.MarkerName)); err != nil {
		t.Fatalf("marker: %v", err)
	}
	loaded, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Repo.RepoID != id || loaded.Repo.Path != repo {
		t.Fatalf("config = %+v", loaded.Repo)
	}
	drives, err = d.Drives()
	if err != nil {
		t.Fatal(err)
	}
	if !drives[0].Initialized || drives[0].RepoID != id {
		t.Fatalf("drives after init = %+v", drives[0])
	}
}

func TestUseStoreRequiresInitializedDrive(t *testing.T) {
	mount := t.TempDir()
	repo := t.TempDir()
	d := New(Config{RepoPath: repo, NoTray: true},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	d.mounts = func() ([]device.Mount, error) {
		return []device.Mount{{Point: mount, FSType: "ext4"}}, nil
	}
	d.checkFS = func(string) error { return nil }
	d.mountFilter = func(string) bool { return true }
	if _, err := d.UseStore(mount, repo); err == nil {
		t.Fatal("UseStore accepted a drive without a store")
	}
}

func TestUseStoreReattachesImmediately(t *testing.T) {
	mount := t.TempDir()
	storeRoot := filepath.Join(mount, "WALKY")
	st, err := store.Init(storeRoot, false)
	if err != nil {
		t.Fatal(err)
	}
	marker := st.Marker()
	repo := t.TempDir()
	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	cfg := config.Default()
	cfg.Repo.Path = repo
	cfg.Device.AutoSync = false
	if err := config.Save(cfgPath, cfg); err != nil {
		t.Fatal(err)
	}
	d := New(Config{RepoPath: repo, ConfigPath: cfgPath, NoTray: true},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	d.mounts = func() ([]device.Mount, error) {
		return []device.Mount{{Point: mount, FSType: "ext4"}}, nil
	}
	d.checkFS = func(string) error { return nil }
	d.mountFilter = func(string) bool { return true }
	d.scanStores = func() ([]device.Device, error) {
		return []device.Device{{
			MountPoint: mount, StoreRoot: storeRoot, Label: "test", Marker: marker,
		}}, nil
	}

	id, err := d.UseStore(mount, repo)
	if err != nil {
		t.Fatalf("UseStore: %v", err)
	}
	if id != marker.RepoID {
		t.Fatalf("repo id = %q, want %q", id, marker.RepoID)
	}
	if d.dev == nil || d.dev.StoreRoot != storeRoot {
		t.Fatalf("store was not attached: %+v", d.dev)
	}
}

func TestRecoveryMergesWithoutDeleting(t *testing.T) {
	root := t.TempDir()
	storeRoot := filepath.Join(root, "store")
	st, err := store.Init(storeRoot, false)
	if err != nil {
		t.Fatal(err)
	}
	newID := st.Marker().RepoID
	repo := filepath.Join(root, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "keep.txt"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(root, "config.toml")
	cfg := config.Default()
	cfg.Repo.Path = repo
	cfg.Repo.RepoID = "old-repo"
	cfg.Device.AutoSync = false
	if err := config.Save(cfgPath, cfg); err != nil {
		t.Fatal(err)
	}
	basePath := filepath.Join(root, "base")
	trash := filepath.Join(root, "trash")
	d := New(Config{
		RepoPath: repo, RepoID: "old-repo", ConfigPath: cfgPath,
		BasePath: basePath, TrashDir: trash, IndexPath: filepath.Join(root, "index.db"),
		Machine: "test", Mode: "two-way", Conflict: "copy", NoTray: true,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	d.checkFS = func(string) error { return nil }
	dev := device.Device{
		MountPoint: storeRoot, StoreRoot: storeRoot, Label: "test", Marker: st.Marker(),
	}
	d.attach(dev)
	if got := d.Status().State; got != StateNeedsRecovery {
		t.Fatalf("state = %q, want needs_recovery", got)
	}
	d.scanStores = func() ([]device.Device, error) { return []device.Device{dev}, nil }

	if err := d.StartRecovery(); err != nil {
		t.Fatalf("StartRecovery: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if st := d.Status().State; st == StateIdle {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	stt := d.Status()
	if stt.State != StateIdle {
		t.Fatalf("state after recovery = %q (%s)", stt.State, stt.LastError)
	}
	loaded, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Repo.RepoID != newID {
		t.Fatalf("config repo_id = %q, want %q", loaded.Repo.RepoID, newID)
	}
	latest, _, err := st.Latest()
	if err != nil {
		t.Fatalf("latest: %v", err)
	}
	found := false
	for _, e := range latest.Entries {
		if e.Path == "keep.txt" {
			found = true
		}
	}
	if !found {
		t.Fatal("recovery did not upload keep.txt")
	}
}
