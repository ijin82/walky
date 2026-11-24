// Package api exposes the daemon over D-Bus.
package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/godbus/dbus/v5"

	"walky/internal/daemon"
)

// ErrAlreadyRunning means another daemon owns the bus name.
var ErrAlreadyRunning = errors.New("another walky daemon is already running")

const (
	// BusName is the well-known name owned by the daemon.
	BusName = "io.github.ijin.Walky1"
	// ObjectPath is where the service object is exported.
	ObjectPath = dbus.ObjectPath("/io/github/ijin/Walky")
	// Interface is the service interface.
	Interface = "io.github.ijin.Walky1"
)

// Service implements both the D-Bus interface and the daemon's Emitter.
type Service struct {
	conn *dbus.Conn
	d    *daemon.Daemon
	log  *slog.Logger

	closeOnce sync.Once
	closeErr  error
}

// New connects to the session bus, claims the well-known name and exports the
// object. It fails when another instance already owns the name.
func New(d *daemon.Daemon, log *slog.Logger) (*Service, error) {
	if log == nil {
		log = slog.Default()
	}
	conn, err := dbus.SessionBus()
	if err != nil {
		return nil, fmt.Errorf("session bus: %w", err)
	}
	reply, err := conn.RequestName(BusName, dbus.NameFlagDoNotQueue)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if reply != dbus.RequestNameReplyPrimaryOwner {
		_ = conn.Close()
		return nil, fmt.Errorf("%w: %s", ErrAlreadyRunning, BusName)
	}
	s := &Service{conn: conn, d: d, log: log}
	if err := conn.Export(s, ObjectPath, Interface); err != nil {
		_, _ = conn.ReleaseName(BusName)
		_ = conn.Close()
		return nil, fmt.Errorf("export %s: %w", ObjectPath, err)
	}
	d.SetEmitter(s)
	return s, nil
}

// Close releases the bus name and the connection. It is safe to call from
// several goroutines and more than once.
func (s *Service) Close() error {
	s.closeOnce.Do(func() {
		if s.conn == nil {
			return
		}
		_, _ = s.conn.ReleaseName(BusName)
		s.closeErr = s.conn.Close()
	})
	return s.closeErr
}

// Run blocks until ctx is cancelled, then closes the service.
func (s *Service) Run(ctx context.Context) {
	<-ctx.Done()
	_ = s.Close()
}

// SyncNow starts a sync; wait blocks until it finishes.
func (s *Service) SyncNow(wait bool) *dbus.Error {
	if err := s.d.SyncNow(wait); err != nil {
		return dbus.MakeFailedError(err)
	}
	return nil
}

// Pause toggles automatic synchronization.
func (s *Service) Pause(paused bool) *dbus.Error {
	s.d.Pause(paused)
	return nil
}

// GetStatus returns the daemon state as a variant map.
func (s *Service) GetStatus() (map[string]dbus.Variant, *dbus.Error) {
	return statusMap(s.d.Status()), nil
}

// GetDrives lists removable ext4 mounts for the setup wizard.
func (s *Service) GetDrives() ([]daemon.Drive, *dbus.Error) {
	drives, err := s.d.Drives()
	if err != nil {
		return nil, dbus.MakeFailedError(err)
	}
	return drives, nil
}

// InitStore creates a store on a listed drive and binds it to a repository.
func (s *Service) InitStore(mountPoint string, repoPath string) (string, *dbus.Error) {
	id, err := s.d.InitStore(mountPoint, repoPath)
	if err != nil {
		return id, dbus.MakeFailedError(err)
	}
	return id, nil
}

// UseStore binds an existing store to a repository.
func (s *Service) UseStore(mountPoint string, repoPath string) (string, *dbus.Error) {
	id, err := s.d.UseStore(mountPoint, repoPath)
	if err != nil {
		return id, dbus.MakeFailedError(err)
	}
	return id, nil
}

// PreviewRecovery summarizes the additive merge for a foreign store.
func (s *Service) PreviewRecovery() (daemon.RecoveryPreview, *dbus.Error) {
	p, err := s.d.PreviewRecovery(context.Background())
	if err != nil {
		return daemon.RecoveryPreview{}, dbus.MakeFailedError(err)
	}
	return p, nil
}

// StartRecovery starts the additive merge in the background.
func (s *Service) StartRecovery() *dbus.Error {
	if err := s.d.StartRecovery(); err != nil {
		return dbus.MakeFailedError(err)
	}
	return nil
}

// GetSettings returns the editable configuration.
func (s *Service) GetSettings() (map[string]dbus.Variant, *dbus.Error) {
	st := s.d.Settings()
	return map[string]dbus.Variant{
		"excludes":     dbus.MakeVariant(st.Excludes),
		"daily":        dbus.MakeVariant(int32(st.Retention.Daily)),
		"weekly":       dbus.MakeVariant(int32(st.Retention.Weekly)),
		"monthly":      dbus.MakeVariant(int32(st.Retention.Monthly)),
		"trash_days":   dbus.MakeVariant(int32(st.Retention.TrashDays)),
		"machine_days": dbus.MakeVariant(int32(st.Retention.MachineDays)),
		"conflict":     dbus.MakeVariant(st.Conflict),
	}, nil
}

// SetSettings validates, persists and applies the editable configuration.
func (s *Service) SetSettings(excludes []string, daily int32, weekly int32, monthly int32, trashDays int32, conflict string) *dbus.Error {
	err := s.d.UpdateSettings(daemon.Settings{
		Excludes: excludes,
		Retention: daemon.RetentionSettings{
			Daily: int(daily), Weekly: int(weekly),
			Monthly: int(monthly), TrashDays: int(trashDays),
		},
		Conflict: conflict,
	})
	if err != nil {
		return dbus.MakeFailedError(err)
	}
	return nil
}

// ReloadConfig re-reads the configuration file.
func (s *Service) ReloadConfig() *dbus.Error {
	if err := s.d.ReloadConfig(); err != nil {
		return dbus.MakeFailedError(err)
	}
	return nil
}

// GetEvents returns the most recent activity, newest first.
func (s *Service) GetEvents(limit uint32) ([]daemon.Event, *dbus.Error) {
	return s.d.Events(int(limit)), nil
}

// Eject unmounts and powers off the current drive.
func (s *Service) Eject() *dbus.Error {
	if err := s.d.Eject(context.Background()); err != nil {
		return dbus.MakeFailedError(err)
	}
	return nil
}

// Verify checks the current store.
func (s *Service) Verify(full bool) (string, *dbus.Error) {
	summary, err := s.d.Verify(full)
	if err != nil {
		return summary, dbus.MakeFailedError(err)
	}
	return summary, nil
}

// StateChanged emits the state signal.
func (s *Service) StateChanged(state string) {
	s.emit("StateChanged", state)
}

// Progress emits the progress signal.
func (s *Service) Progress(done, total int, bytes int64) {
	s.emit("Progress", uint32(done), uint32(total), bytes)
}

// FileDone emits the per-file signal.
func (s *Service) FileDone(path, op, result string) {
	s.emit("FileDone", path, op, result)
}

func (s *Service) emit(member string, args ...any) {
	if s.conn == nil {
		return
	}
	if err := s.conn.Emit(ObjectPath, Interface+"."+member, args...); err != nil {
		s.log.Warn("emit failed", "signal", member, "err", err)
	}
}

func statusMap(st daemon.Status) map[string]dbus.Variant {
	m := map[string]dbus.Variant{
		"state":          dbus.MakeVariant(st.State),
		"device":         dbus.MakeVariant(st.Device),
		"store_root":     dbus.MakeVariant(st.StoreRoot),
		"mount_point":    dbus.MakeVariant(st.MountPoint),
		"repo_id":        dbus.MakeVariant(st.RepoID),
		"last_sync":      dbus.MakeVariant(st.LastSync),
		"generation":     dbus.MakeVariant(st.Generation),
		"pending":        dbus.MakeVariant(uint32(st.Pending)),
		"conflicts":      dbus.MakeVariant(uint32(st.Conflicts)),
		"free_bytes":     dbus.MakeVariant(st.FreeBytes),
		"paused":         dbus.MakeVariant(st.Paused),
		"progress_done":  dbus.MakeVariant(uint32(st.ProgressDone)),
		"progress_total": dbus.MakeVariant(uint32(st.ProgressTotal)),
		"progress_bytes": dbus.MakeVariant(st.ProgressBytes),
		"last_error":     dbus.MakeVariant(st.LastError),
	}
	return m
}
