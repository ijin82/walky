package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/godbus/dbus/v5"

	"walky/internal/api"
)

// errServiceUnavailable reports that the daemon is not reachable over D-Bus.
var errServiceUnavailable = errors.New("walky daemon is not running")

func serviceCall(timeout time.Duration, method string, args ...any) ([]any, error) {
	conn, err := dbus.SessionBus()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errServiceUnavailable, err)
	}
	obj := conn.Object(api.BusName, api.ObjectPath)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	call := obj.CallWithContext(ctx, api.Interface+"."+method, 0, args...)
	if call.Err != nil {
		return nil, classifyCallError(call.Err)
	}
	return call.Body, nil
}

// classifyCallError separates "daemon is not running" from errors returned by
// a live daemon, so the CLI does not mislabel real failures.
func classifyCallError(err error) error {
	var de dbus.Error
	if errors.As(err, &de) {
		return classifyDBusError(de)
	}
	var dep *dbus.Error
	if errors.As(err, &dep) && dep != nil {
		return classifyDBusError(*dep)
	}
	return fmt.Errorf("%w: %v", errServiceUnavailable, err)
}

func classifyDBusError(de dbus.Error) error {
	switch de.Name {
	case "org.freedesktop.DBus.Error.ServiceUnknown",
		"org.freedesktop.DBus.Error.NameHasNoOwner",
		"org.freedesktop.DBus.Error.Disconnected",
		"org.freedesktop.DBus.Error.NoReply",
		"org.freedesktop.DBus.Error.Timeout":
		return fmt.Errorf("%w: %s", errServiceUnavailable, de.Name)
	}
	if len(de.Body) > 0 {
		if msg, ok := de.Body[0].(string); ok && msg != "" {
			return errors.New(msg)
		}
	}
	return errors.New(de.Name)
}

func serviceStatus() (map[string]any, error) {
	body, err := serviceCall(3*time.Second, "GetStatus")
	if err != nil {
		return nil, err
	}
	if len(body) == 0 {
		return nil, errors.New("empty status reply")
	}
	raw, ok := body[0].(map[string]dbus.Variant)
	if !ok {
		return nil, fmt.Errorf("unexpected status type %T", body[0])
	}
	out := make(map[string]any, len(raw))
	for k, v := range raw {
		out[k] = v.Value()
	}
	return out, nil
}

func serviceSync(wait bool) error {
	_, err := serviceCall(24*time.Hour, "SyncNow", wait)
	return err
}

func serviceEject() error {
	_, err := serviceCall(30*time.Second, "Eject")
	return err
}
