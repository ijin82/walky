package device

import (
	"context"

	"github.com/godbus/dbus/v5"
)

// WatchSleep reports suspend/resume transitions from logind. A true value means
// the machine is about to suspend; false means it resumed.
func WatchSleep(ctx context.Context) (<-chan bool, error) {
	conn, err := dbus.SystemBus()
	if err != nil {
		return nil, err
	}
	if err := conn.AddMatchSignal(
		dbus.WithMatchObjectPath("/org/freedesktop/login1"),
		dbus.WithMatchInterface("org.freedesktop.login1.Manager"),
		dbus.WithMatchMember("PrepareForSleep"),
	); err != nil {
		_ = conn.Close()
		return nil, err
	}
	signals := make(chan *dbus.Signal, 4)
	conn.Signal(signals)

	out := make(chan bool, 4)
	go func() {
		defer close(out)
		defer conn.Close()
		for {
			select {
			case <-ctx.Done():
				return
			case sig, ok := <-signals:
				if !ok {
					return
				}
				if len(sig.Body) == 0 {
					continue
				}
				sleeping, _ := sig.Body[0].(bool)
				select {
				case out <- sleeping:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out, nil
}
