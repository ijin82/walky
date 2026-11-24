package device

import (
	"testing"

	"github.com/godbus/dbus/v5"
)

func TestFindMountWithNULTerminatedPath(t *testing.T) {
	objects := map[dbus.ObjectPath]map[string]map[string]dbus.Variant{
		"/org/freedesktop/UDisks2/block_devices/sdd1": {
			"org.freedesktop.UDisks2.Block": {
				"Drive": dbus.MakeVariant(dbus.ObjectPath("/org/freedesktop/UDisks2/drives/USB")),
			},
		},
		"/org/freedesktop/UDisks2/block_devices/dm_2d0": {
			"org.freedesktop.UDisks2.Block": {
				"Drive":               dbus.MakeVariant(dbus.ObjectPath("/")),
				"CryptoBackingDevice": dbus.MakeVariant(dbus.ObjectPath("/org/freedesktop/UDisks2/block_devices/sdd1")),
			},
			"org.freedesktop.UDisks2.Filesystem": {
				"MountPoints": dbus.MakeVariant([][]byte{
					[]byte("/run/media/user/ijin_c\x00"),
				}),
			},
		},
	}
	fs, drive, err := FindMount(objects, "/run/media/user/ijin_c")
	if err != nil {
		t.Fatal(err)
	}
	if fs != "/org/freedesktop/UDisks2/block_devices/dm_2d0" {
		t.Fatalf("filesystem path = %q", fs)
	}
	if drive != "/org/freedesktop/UDisks2/drives/USB" {
		t.Fatalf("drive path = %q", drive)
	}
	if _, _, err := FindMount(objects, "/run/media/user/other"); err == nil {
		t.Fatal("unexpected mount accepted")
	}
}
