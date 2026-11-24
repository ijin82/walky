package device

import (
	"context"
	"fmt"
	"strings"

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
	// A LUKS mapping keeps the physical drive busy; close it before powering
	// off. Both steps are best-effort: once the filesystem is unmounted it is
	// safe to unplug even when polkit refuses the lock or power-off.
	if backing := encryptedBacking(objects, fsPath); backing != "" {
		_ = conn.Object(udisksName, backing).CallWithContext(ctx,
			"org.freedesktop.UDisks2.Encrypted.Lock", 0, map[string]dbus.Variant{}).Err
	}
	if drivePath != "" && drivePath != "/" {
		_ = conn.Object(udisksName, drivePath).CallWithContext(ctx,
			"org.freedesktop.UDisks2.Drive.PowerOff", 0, map[string]dbus.Variant{}).Err
	}
	return nil
}

// encryptedBacking returns the LUKS backing object for a cleartext filesystem.
func encryptedBacking(objects map[dbus.ObjectPath]map[string]map[string]dbus.Variant, fsPath dbus.ObjectPath) dbus.ObjectPath {
	ifaces, ok := objects[fsPath]
	if !ok {
		return ""
	}
	block, ok := ifaces["org.freedesktop.UDisks2.Block"]
	if !ok {
		return ""
	}
	cb, ok := block["CryptoBackingDevice"]
	if !ok {
		return ""
	}
	backing, _ := cb.Value().(dbus.ObjectPath)
	if backing == "" || backing == "/" {
		return ""
	}
	if _, ok := objects[backing]["org.freedesktop.UDisks2.Encrypted"]; !ok {
		return ""
	}
	return backing
}

// blockDrive returns the physical drive for a block device. LUKS cleartext
// devices report "/" as their drive, so follow CryptoBackingDevice to the
// underlying encrypted device in that case.
func blockDrive(objects map[dbus.ObjectPath]map[string]map[string]dbus.Variant, ifaces map[string]map[string]dbus.Variant) dbus.ObjectPath {
	block, ok := ifaces["org.freedesktop.UDisks2.Block"]
	if !ok {
		return ""
	}
	var drive dbus.ObjectPath
	if d, ok := block["Drive"]; ok {
		drive, _ = d.Value().(dbus.ObjectPath)
	}
	if drive != "" && drive != "/" {
		return drive
	}
	if cb, ok := block["CryptoBackingDevice"]; ok {
		backing, _ := cb.Value().(dbus.ObjectPath)
		if backing != "" && backing != "/" {
			if b, ok := objects[backing]["org.freedesktop.UDisks2.Block"]; ok {
				if d, ok := b["Drive"]; ok {
					if v, ok := d.Value().(dbus.ObjectPath); ok {
						return v
					}
				}
			}
		}
	}
	return drive
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
			// udisks2 returns NUL-terminated byte arrays.
			if strings.TrimRight(string(p), "\x00") != mountPoint {
				continue
			}
			drive := blockDrive(objects, ifaces)
			return path, drive, nil
		}
	}
	return "", "", fmt.Errorf("udisks2 has no filesystem mounted at %s", mountPoint)
}
