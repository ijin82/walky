package device

import (
	"context"
	"fmt"

	"github.com/godbus/dbus/v5"
)

// DefaultLabel is the filesystem label Walky automounts.
const DefaultLabel = "WALKY"

// MountLabeled asks udisks2 to mount every unmounted filesystem with the given
// label and returns how many mounts succeeded. It never unlocks LUKS: encrypted
// drives are handled by the desktop, and the ext4 filesystem only becomes
// visible after the user unlocks it.
func MountLabeled(ctx context.Context, label string) (int, error) {
	conn, err := dbus.SystemBus()
	if err != nil {
		return 0, fmt.Errorf("system bus: %w", err)
	}
	obj := conn.Object(udisksName, udisksRoot)
	var objects map[dbus.ObjectPath]map[string]map[string]dbus.Variant
	if err := obj.CallWithContext(ctx, "org.freedesktop.DBus.ObjectManager.GetManagedObjects", 0).Store(&objects); err != nil {
		return 0, fmt.Errorf("list udisks objects: %w", err)
	}

	mounted := 0
	for _, path := range FindUnmounted(objects, label) {
		options := map[string]dbus.Variant{"options": dbus.MakeVariant("noatime")}
		call := conn.Object(udisksName, path).CallWithContext(ctx,
			"org.freedesktop.UDisks2.Filesystem.Mount", 0, options)
		if call.Err != nil {
			// Polkit denial or a race with the desktop are expected; try the
			// remaining devices.
			continue
		}
		mounted++
	}
	return mounted, nil
}

// FindUnmounted returns filesystems with the requested label that have no
// mount points. It is pure so it can be tested without udisks.
func FindUnmounted(objects map[dbus.ObjectPath]map[string]map[string]dbus.Variant, label string) []dbus.ObjectPath {
	var out []dbus.ObjectPath
	for path, ifaces := range objects {
		fs, ok := ifaces["org.freedesktop.UDisks2.Filesystem"]
		if !ok {
			continue
		}
		block, ok := ifaces["org.freedesktop.UDisks2.Block"]
		if !ok {
			continue
		}
		id, ok := block["IdLabel"]
		if !ok || id.Value() != label {
			continue
		}
		mp, ok := fs["MountPoints"]
		if !ok {
			continue
		}
		points, ok := mp.Value().([][]byte)
		if !ok || len(points) > 0 {
			continue
		}
		out = append(out, path)
	}
	return out
}
