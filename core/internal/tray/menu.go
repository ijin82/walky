package tray

import (
	"fmt"
	"sync"

	"github.com/godbus/dbus/v5"
)

// Menu item identifiers.
const (
	idStatus = 1
	idSync   = 2
	idPause  = 3
	idOpen   = 4
	idVerify = 5
	idSep    = 6
	idQuit   = 7
)

type layoutItem struct {
	ID       int32
	Props    map[string]dbus.Variant
	Children []dbus.Variant
}

type propertyEntry struct {
	ID    int32
	Props map[string]dbus.Variant
}

type menuEvent struct {
	ID        int32
	EventID   string
	Data      dbus.Variant
	Timestamp uint32
}

type menu struct {
	mu       sync.Mutex
	actions  Actions
	snapshot func() Snapshot
	revision uint32
	emit     func(uint32)
}

func newMenu(actions Actions, snapshot func() Snapshot, emit func(uint32)) *menu {
	return &menu{actions: actions, snapshot: snapshot, emit: emit, revision: 1}
}

func (m *menu) currentRevision() uint32 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.revision
}

// bump invalidates the layout and notifies the host.
func (m *menu) bump() {
	m.mu.Lock()
	m.revision++
	rev := m.revision
	emit := m.emit
	m.mu.Unlock()
	if emit != nil {
		emit(rev)
	}
}

func (m *menu) layout(parentID int32, depth int32) (layoutItem, bool) {
	if parentID != 0 && parentID != -1 {
		// Flat menu: only the root has children.
		return layoutItem{}, false
	}
	snap := m.snapshot()
	pauseLabel := "Pause"
	if snap.Paused {
		pauseLabel = "Resume"
	}
	status := "No drive"
	if snap.Device != "" {
		status = fmt.Sprintf("%s · generation %d", snap.Device, snap.Generation)
	}
	items := []layoutItem{
		{ID: idStatus, Props: disabledProps("Walky: " + stateText(snap.State))},
		{ID: idSync, Props: actionProps("Sync now")},
		{ID: idPause, Props: actionProps(pauseLabel)},
		{ID: idOpen, Props: actionProps("Open Walky")},
		{ID: idVerify, Props: actionProps("Verify integrity")},
		{ID: idSep, Props: map[string]dbus.Variant{"type": dbus.MakeVariant("separator")}},
		{ID: idQuit, Props: actionProps("Quit")},
	}
	children := make([]dbus.Variant, 0, len(items))
	for _, item := range items {
		children = append(children, dbus.MakeVariant(item))
	}
	_ = status
	root := layoutItem{
		ID:       0,
		Props:    map[string]dbus.Variant{"children-display": dbus.MakeVariant("submenu")},
		Children: children,
	}
	return root, true
}

func actionProps(label string) map[string]dbus.Variant {
	return map[string]dbus.Variant{
		"label":   dbus.MakeVariant(label),
		"enabled": dbus.MakeVariant(true),
		"visible": dbus.MakeVariant(true),
	}
}

func disabledProps(label string) map[string]dbus.Variant {
	return map[string]dbus.Variant{
		"label":   dbus.MakeVariant(label),
		"enabled": dbus.MakeVariant(false),
		"visible": dbus.MakeVariant(true),
	}
}

func (m *menu) GetLayout(parentID int32, recursionDepth int32, propertyNames []string) (uint32, layoutItem, *dbus.Error) {
	root, ok := m.layout(parentID, recursionDepth)
	if !ok {
		return m.currentRevision(), layoutItem{}, nil
	}
	return m.currentRevision(), root, nil
}

func (m *menu) GetGroupProperties(ids []int32, propertyNames []string) ([]propertyEntry, *dbus.Error) {
	root, _ := m.layout(0, -1)
	out := []propertyEntry{{ID: root.ID, Props: root.Props}}
	for _, child := range root.Children {
		item := child.Value().(layoutItem)
		out = append(out, propertyEntry{ID: item.ID, Props: item.Props})
	}
	return out, nil
}

func (m *menu) GetProperty(id int32, name string) (dbus.Variant, *dbus.Error) {
	root, _ := m.layout(0, -1)
	if id == 0 {
		if v, ok := root.Props[name]; ok {
			return v, nil
		}
	}
	for _, child := range root.Children {
		item := child.Value().(layoutItem)
		if item.ID == id {
			if v, ok := item.Props[name]; ok {
				return v, nil
			}
		}
	}
	return dbus.MakeVariant(""), nil
}

func (m *menu) Event(id int32, eventID string, data dbus.Variant, timestamp uint32) *dbus.Error {
	if eventID != "clicked" {
		return nil
	}
	switch id {
	case idSync:
		if m.actions.SyncNow != nil {
			m.actions.SyncNow()
		}
	case idPause:
		if m.actions.TogglePause != nil {
			m.actions.TogglePause()
		}
	case idOpen:
		if m.actions.OpenUI != nil {
			_ = m.actions.OpenUI()
		}
	case idVerify:
		if m.actions.Verify != nil {
			m.actions.Verify()
		}
	case idQuit:
		if m.actions.Quit != nil {
			m.actions.Quit()
		}
	}
	m.bump()
	return nil
}

func (m *menu) EventGroup(events []menuEvent) ([]int32, *dbus.Error) {
	var failed []int32
	for _, ev := range events {
		if err := m.Event(ev.ID, ev.EventID, ev.Data, ev.Timestamp); err != nil {
			failed = append(failed, ev.ID)
		}
	}
	return failed, nil
}

func (m *menu) AboutToShow(id int32) (bool, *dbus.Error) {
	return false, nil
}

func (m *menu) AboutToShowGroup(ids []int32) ([]int32, []int32, *dbus.Error) {
	return nil, nil, nil
}

func stateText(state string) string {
	switch state {
	case "idle":
		return "up to date"
	case "syncing":
		return "syncing"
	case "paused":
		return "paused"
	case "error":
		return "error"
	case "no_device":
		return "no drive"
	default:
		return state
	}
}
