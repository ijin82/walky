package main

import (
	"errors"
	"testing"

	"github.com/godbus/dbus/v5"
)

func TestClassifyCallError(t *testing.T) {
	err := classifyDBusError(dbus.Error{Name: "org.freedesktop.DBus.Error.ServiceUnknown"})
	if !errors.Is(err, errServiceUnavailable) {
		t.Fatalf("service unknown classified as %v", err)
	}

	err = classifyDBusError(dbus.Error{
		Name: "io.github.ijin.Walky1.Error",
		Body: []any{"no walky drive is attached"},
	})
	if err.Error() != "no walky drive is attached" {
		t.Fatalf("remote error = %q", err.Error())
	}
	if errors.Is(err, errServiceUnavailable) {
		t.Fatal("remote failure misclassified as missing daemon")
	}
}
