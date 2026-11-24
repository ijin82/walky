package device

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"

	"walky/internal/store"
)

func TestReadMountsAndUnescape(t *testing.T) {
	p := filepath.Join(t.TempDir(), "mountinfo")
	content := `36 25 8:1 / /run/media/u/My\040Disk rw,relatime shared:1 - ext4 /dev/sdb1 rw
37 25 0:5 / /proc rw - proc proc rw
`
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	mounts, err := ReadMounts(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(mounts) != 2 {
		t.Fatalf("got %d mounts, want 2", len(mounts))
	}
	if mounts[0].Point != "/run/media/u/My Disk" {
		t.Fatalf("mount point = %q", mounts[0].Point)
	}
	if mounts[0].FSType != "ext4" || mounts[0].Source != "/dev/sdb1" {
		t.Fatalf("mount = %+v", mounts[0])
	}
}

func TestScanFindsStore(t *testing.T) {
	mountPoint := t.TempDir()
	storeRoot := filepath.Join(mountPoint, "WALKY")
	if err := os.MkdirAll(storeRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Init(storeRoot, false); err != nil {
		t.Fatal(err)
	}
	w := NewWatcherWithSource(time.Second, func() ([]Mount, error) {
		return []Mount{{Point: mountPoint, FSType: "ext4"}}, nil
	})
	devices, err := w.Scan()
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 1 {
		t.Fatalf("got %d devices, want 1", len(devices))
	}
	if devices[0].StoreRoot != storeRoot || devices[0].MountPoint != mountPoint {
		t.Fatalf("device = %+v", devices[0])
	}
	if devices[0].Marker.RepoID == "" {
		t.Fatal("marker not read")
	}
}

func TestScanIgnoresForeignMounts(t *testing.T) {
	mountPoint := t.TempDir()
	w := NewWatcherWithSource(time.Second, func() ([]Mount, error) {
		return []Mount{{Point: mountPoint}, {Point: "/proc"}, {Point: "/dev"}}, nil
	})
	devices, err := w.Scan()
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 0 {
		t.Fatalf("unexpected devices: %+v", devices)
	}
}

func TestWatcherAttachAndDetach(t *testing.T) {
	mountPoint := t.TempDir()
	storeRoot := filepath.Join(mountPoint, "WALKY")
	if err := os.MkdirAll(storeRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Init(storeRoot, false); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var mounts []Mount
	w := NewWatcherWithSource(10*time.Millisecond, func() ([]Mount, error) {
		mu.Lock()
		defer mu.Unlock()
		return append([]Mount(nil), mounts...), nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := make(chan Event, 8)
	go func() { _ = w.Run(ctx, events) }()

	mu.Lock()
	mounts = []Mount{{Point: mountPoint}}
	mu.Unlock()

	if ev := waitEvent(t, events); ev.Kind != Attached {
		t.Fatalf("event = %+v, want attach", ev)
	}

	// Removing the marker makes the store disappear from the watcher.
	if err := os.Remove(filepath.Join(storeRoot, store.MarkerName)); err != nil {
		t.Fatal(err)
	}
	if ev := waitEvent(t, events); ev.Kind != Detached {
		t.Fatalf("event = %+v, want detach", ev)
	}
}

func waitEvent(t *testing.T, events <-chan Event) Event {
	t.Helper()
	select {
	case ev := <-events:
		return ev
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for device event")
		return Event{}
	}
}

func TestFindMount(t *testing.T) {
	fsPath := dbus.ObjectPath("/org/freedesktop/UDisks2/block_devices/sdb1")
	drivePath := dbus.ObjectPath("/org/freedesktop/UDisks2/drives/USB_1")
	objects := map[dbus.ObjectPath]map[string]map[string]dbus.Variant{
		fsPath: {
			"org.freedesktop.UDisks2.Filesystem": {
				"MountPoints": dbus.MakeVariant([][]byte{[]byte("/run/media/u/WALKY")}),
			},
			"org.freedesktop.UDisks2.Block": {
				"Drive": dbus.MakeVariant(drivePath),
			},
		},
		"/org/freedesktop/UDisks2/block_devices/sda1": {
			"org.freedesktop.UDisks2.Filesystem": {
				"MountPoints": dbus.MakeVariant([][]byte{[]byte("/")}),
			},
		},
	}
	gotFS, gotDrive, err := FindMount(objects, "/run/media/u/WALKY")
	if err != nil {
		t.Fatal(err)
	}
	if gotFS != fsPath || gotDrive != drivePath {
		t.Fatalf("got %s/%s, want %s/%s", gotFS, gotDrive, fsPath, drivePath)
	}
	if _, _, err := FindMount(objects, "/missing"); err == nil {
		t.Fatal("expected an error for a missing mount")
	}
}
