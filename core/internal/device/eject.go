package device

import (
	"context"
	"fmt"

	"github.com/godbus/dbus/v5"
)

const (
	udisksName = "org.freedesktop.UDisks2"
	udisksRoot = "/org/freedesktop/UDisks2"
)

// Eject unmounts the filesystem at mountPoint through udisks2 and powers the
// drive off when udisks knows its drive. Callers must ensure no sync is running.
func Eject(ctx context.Context, mountPoint string) error {
	conn, err := dbus.SystemBus()
	if err != nil {
		return fmt.Errorf("system bus: %w", err)
	}
	obj := conn.Object(udisksName, udisksRoot)
	var objects map[dbus.ObjectPath]map[string]map[string]dbus.Variant
	if err := obj.CallWithContext(ctx, "org.freedesktop.DBus.ObjectManager.GetManagedObjects", 0).Store(&objects); err != nil {
		return fmt.Errorf("list udisks objects: %w", err)
	}
	fsPath, drivePath, err := FindMount(objects, mountPoint)
	if err != nil {
		return err
	}
	fs := conn.Object(udisksName, fsPath)
	if err := fs.CallWithContext(ctx, "org.freedesktop.UDisks2.Filesystem.Unmount", 0,
		map[string]dbus.Variant{"force": dbus.MakeVariant(false)}).Err; err != nil {
		return fmt.Errorf("unmount %s: %w", mountPoint, err)
	}
	if drivePath != "" {
		drive := conn.Object(udisksName, drivePath)
		if err := drive.CallWithContext(ctx, "org.freedesktop.UDisks2.Drive.PowerOff", 0,
			map[string]dbus.Variant{}).Err; err != nil {
			return fmt.Errorf("power off drive: %w", err)
		}
	}
	return nil
}

// FindMount locates the udisks filesystem object for a mount point and, when
// available, the drive object that owns it.
func FindMount(objects map[dbus.ObjectPath]map[string]map[string]dbus.Variant, mountPoint string) (dbus.ObjectPath, dbus.ObjectPath, error) {
	for path, ifaces := range objects {
		props, ok := ifaces["org.freedesktop.UDisks2.Filesystem"]
		if !ok {
			continue
		}
		raw, ok := props["MountPoints"]
		if !ok {
			continue
		}
		points, ok := raw.Value().([][]byte)
		if !ok {
			continue
		}
		for _, p := range points {
			if string(p) != mountPoint {
				continue
			}
			var drive dbus.ObjectPath
			if block, ok := ifaces["org.freedesktop.UDisks2.Block"]; ok {
				if d, ok := block["Drive"]; ok {
					drive, _ = d.Value().(dbus.ObjectPath)
				}
			}
			return path, drive, nil
		}
	}
	return "", "", fmt.Errorf("udisks2 has no filesystem mounted at %s", mountPoint)
}
