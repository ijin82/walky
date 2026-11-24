package device

import (
	"testing"

	"github.com/godbus/dbus/v5"
)

func TestFindUnmounted(t *testing.T) {
	objects := map[dbus.ObjectPath]map[string]map[string]dbus.Variant{
		"/dev/sdb1": {
			"org.freedesktop.UDisks2.Block":      {"IdLabel": dbus.MakeVariant("WALKY")},
			"org.freedesktop.UDisks2.Filesystem": {"MountPoints": dbus.MakeVariant([][]byte{})},
		},
		"/dev/sdb2": {
			"org.freedesktop.UDisks2.Block":      {"IdLabel": dbus.MakeVariant("WALKY")},
			"org.freedesktop.UDisks2.Filesystem": {"MountPoints": dbus.MakeVariant([][]byte{[]byte("/run/media/u/WALKY")})},
		},
		"/dev/sdc1": {
			"org.freedesktop.UDisks2.Block":      {"IdLabel": dbus.MakeVariant("OTHER")},
			"org.freedesktop.UDisks2.Filesystem": {"MountPoints": dbus.MakeVariant([][]byte{})},
		},
		"/dev/sdd1": {
			"org.freedesktop.UDisks2.Block": {"IdLabel": dbus.MakeVariant("WALKY")},
		},
	}
	got := FindUnmounted(objects, "WALKY")
	if len(got) != 1 || got[0] != "/dev/sdb1" {
		t.Fatalf("got %v, want [/dev/sdb1]", got)
	}
	if none := FindUnmounted(objects, "MISSING"); len(none) != 0 {
		t.Fatalf("unexpected candidates: %v", none)
	}
}
