// Package tray exposes Walky as a StatusNotifierItem (AppIndicator) on the
// session bus. The icon lives in the daemon, so it survives closing the UI.
package tray

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/prop"
)

const (
	sniPath     = dbus.ObjectPath("/StatusNotifierItem")
	menuPath    = dbus.ObjectPath("/MenuBar")
	sniIface    = "org.kde.StatusNotifierItem"
	menuIface   = "com.canonical.dbusmenu"
	watcherName = "org.kde.StatusNotifierWatcher"
	watcherPath = dbus.ObjectPath("/StatusNotifierWatcher")
)

// Snapshot is the state shown in the tray.
type Snapshot struct {
	State      string
	Device     string
	Generation int64
	Paused     bool
}

// Actions are invoked from the tray menu.
type Actions struct {
	Snapshot    func() Snapshot
	SyncNow     func()
	TogglePause func() bool
	OpenUI      func() error
	Verify      func()
	Quit        func()
}

type pixmap struct {
	Width  int32
	Height int32
	Data   []byte
}

type toolTip struct {
	IconName   string
	IconPixmap []pixmap
	Title      string
	Subtitle   string
}

// Tray is a registered StatusNotifierItem.
type Tray struct {
	conn    *dbus.Conn
	name    string
	props   *prop.Properties
	menu    *menu
	actions Actions

	mu   sync.Mutex
	last Snapshot
	done chan struct{}
}

// New registers the item with the StatusNotifierWatcher.
func New(actions Actions) (*Tray, error) {
	if actions.Snapshot == nil {
		actions.Snapshot = func() Snapshot { return Snapshot{State: "no_device"} }
	}
	conn, err := dbus.SessionBus()
	if err != nil {
		return nil, err
	}
	name := fmt.Sprintf("org.kde.StatusNotifierItem-%d-1", os.Getpid())
	reply, err := conn.RequestName(name, dbus.NameFlagDoNotQueue)
	if err != nil {
		return nil, err
	}
	if reply != dbus.RequestNameReplyPrimaryOwner {
		return nil, fmt.Errorf("status notifier name is already owned")
	}

	t := &Tray{conn: conn, name: name, actions: actions, done: make(chan struct{})}
	t.menu = newMenu(actions, actions.Snapshot, func(rev uint32) {
		_ = conn.Emit(menuPath, menuIface+".LayoutUpdated", rev, int32(0))
	})

	if err := conn.Export(t, sniPath, sniIface); err != nil {
		t.Close()
		return nil, err
	}
	if err := conn.Export(t.menu, menuPath, menuIface); err != nil {
		t.Close()
		return nil, err
	}

	props, err := prop.Export(conn, sniPath, prop.Map{
		sniIface: {
			"Category":   {Value: "ApplicationStatus", Emit: prop.EmitTrue},
			"Id":         {Value: "walky"},
			"Title":      {Value: "Walky", Emit: prop.EmitTrue},
			"Status":     {Value: "Active", Emit: prop.EmitTrue},
			"IconName":   {Value: "walky", Emit: prop.EmitTrue},
			"IconPixmap": {Value: []pixmap{}},
			"Menu":       {Value: menuPath},
			"ItemIsMenu": {Value: true},
			"WindowId":   {Value: int32(0)},
			"ToolTip": {
				Value: toolTip{IconName: "walky", IconPixmap: []pixmap{},
					Title: "Walky", Subtitle: "No drive"},
				Emit: prop.EmitTrue,
			},
		},
	})
	if err != nil {
		t.Close()
		return nil, err
	}
	t.props = props

	if _, err := prop.Export(conn, menuPath, prop.Map{
		menuIface: {
			"Version":       {Value: uint32(3)},
			"TextDirection": {Value: "ltr"},
			"Status":        {Value: "normal"},
			"IconThemePath": {Value: []string{}},
		},
	}); err != nil {
		t.Close()
		return nil, err
	}

	watcher := conn.Object(watcherName, watcherPath)
	if err := watcher.Call(watcherName+".RegisterStatusNotifierItem", 0, name).Err; err != nil {
		t.Close()
		return nil, err
	}
	t.refresh()
	return t, nil
}

// Run refreshes the tooltip and menu until ctx is cancelled.
func (t *Tray) Run(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			t.Close()
			return
		case <-ticker.C:
			t.refresh()
		}
	}
}

func (t *Tray) refresh() {
	snap := t.actions.Snapshot()
	t.mu.Lock()
	changed := snap != t.last
	t.last = snap
	t.mu.Unlock()
	if !changed {
		return
	}
	title := "Walky — " + stateText(snap.State)
	subtitle := "No drive"
	if snap.Device != "" {
		subtitle = fmt.Sprintf("%s · generation %d", snap.Device, snap.Generation)
	}
	if t.props != nil {
		t.props.SetMust(sniIface, "Title", title)
		t.props.SetMust(sniIface, "ToolTip", toolTip{
			IconName: "walky", IconPixmap: []pixmap{}, Title: title, Subtitle: subtitle,
		})
	}
	_ = t.conn.Emit(sniPath, sniIface+".NewTitle")
	_ = t.conn.Emit(sniPath, sniIface+".NewToolTip")
	t.menu.bump()
}

// Activate opens the window on a left click.
func (t *Tray) Activate(x, y int32) *dbus.Error {
	if t.actions.OpenUI != nil {
		_ = t.actions.OpenUI()
	}
	return nil
}

// SecondaryActivate is a middle click; behave like Activate.
func (t *Tray) SecondaryActivate(x, y int32) *dbus.Error {
	return t.Activate(x, y)
}

// ContextMenu is a no-op: the menu is exposed separately.
func (t *Tray) ContextMenu(x, y int32) *dbus.Error { return nil }

// Scroll is unused.
func (t *Tray) Scroll(delta int32, orientation string) *dbus.Error { return nil }

// Close unregisters the item.
func (t *Tray) Close() {
	select {
	case <-t.done:
		return
	default:
	}
	close(t.done)
	if t.props != nil {
		t.props.SetMust(sniIface, "Status", "Passive")
	}
	if t.conn != nil {
		_, _ = t.conn.ReleaseName(t.name)
	}
}

// OpenUI launches the desktop client if it is not running.
func OpenUI() error {
	if path, err := exec.LookPath("walky-ui"); err == nil {
		return exec.Command(path).Start()
	}
	home, err := os.UserHomeDir()
	if err == nil {
		candidate := filepath.Join(home, ".local", "bin", "walky-ui")
		if _, statErr := os.Stat(candidate); statErr == nil {
			return exec.Command(candidate).Start()
		}
	}
	return fmt.Errorf("walky-ui was not found in PATH")
}
