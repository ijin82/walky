package tray

import (
	"testing"

	"github.com/godbus/dbus/v5"
)

func itemByID(t *testing.T, root layoutItem, id int32) layoutItem {
	t.Helper()
	for _, child := range root.Children {
		item := child.Value().(layoutItem)
		if item.ID == id {
			return item
		}
	}
	t.Fatalf("menu item %d not found", id)
	return layoutItem{}
}

func label(item layoutItem) string {
	return item.Props["label"].Value().(string)
}

func TestMenuLayoutAndEvents(t *testing.T) {
	paused := false
	var synced, quit, ejected bool
	actions := Actions{
		Snapshot: func() Snapshot {
			return Snapshot{State: "idle", Device: "WALKY", Generation: 3, Paused: paused}
		},
		SyncNow: func() { synced = true },
		Eject:   func() { ejected = true },
		Quit:    func() { quit = true },
		TogglePause: func() bool {
			paused = !paused
			return paused
		},
	}
	m := newMenu(actions, actions.Snapshot, nil)

	rev, root, err := m.GetLayout(0, -1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := label(itemByID(t, root, idStatus)); got != "Walky: up to date" {
		t.Fatalf("status label = %q", got)
	}
	if got := label(itemByID(t, root, idPause)); got != "Pause" {
		t.Fatalf("pause label = %q", got)
	}
	if enabled := itemByID(t, root, idStatus).Props["enabled"].Value().(bool); enabled {
		t.Fatal("status item must be disabled")
	}
	eject := itemByID(t, root, idEject)
	if !eject.Props["enabled"].Value().(bool) {
		t.Fatal("eject must be enabled when a drive is attached")
	}

	if err := m.Event(idSync, "clicked", dbus.MakeVariant(""), 0); err != nil {
		t.Fatal(err)
	}
	if !synced {
		t.Fatal("sync action was not called")
	}
	if err := m.Event(idQuit, "clicked", dbus.MakeVariant(""), 0); err != nil {
		t.Fatal(err)
	}
	if !quit {
		t.Fatal("quit action was not called")
	}
	if err := m.Event(idEject, "clicked", dbus.MakeVariant(""), 0); err != nil {
		t.Fatal(err)
	}
	if !ejected {
		t.Fatal("eject action was not called")
	}

	paused = true
	_, root, _ = m.GetLayout(0, -1, nil)
	if got := label(itemByID(t, root, idPause)); got != "Resume" {
		t.Fatalf("pause label after pause = %q", got)
	}
	if m.currentRevision() <= rev {
		t.Fatal("menu revision did not advance")
	}
}

func TestMenuSeparator(t *testing.T) {
	actions := Actions{Snapshot: func() Snapshot { return Snapshot{State: "no_device"} }}
	m := newMenu(actions, actions.Snapshot, nil)
	_, root, _ := m.GetLayout(0, -1, nil)
	sep := itemByID(t, root, idSep)
	if sep.Props["type"].Value().(string) != "separator" {
		t.Fatalf("separator props = %v", sep.Props)
	}
}
