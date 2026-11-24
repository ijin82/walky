package api

import (
	"encoding/xml"
	"io"
	"log/slog"
	"reflect"
	"testing"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/introspect"

	"walky/internal/daemon"
)

func TestStatusMap(t *testing.T) {
	m := statusMap(daemon.Status{
		State:         daemon.StateIdle,
		Device:        "WALKY",
		StoreRoot:     "/run/media/u/WALKY/WALKY",
		Generation:    7,
		LastSync:      1700000000,
		FreeBytes:     1 << 30,
		ProgressDone:  3,
		ProgressTotal: 10,
		Paused:        true,
	})
	if got := m["state"].Value(); got != daemon.StateIdle {
		t.Fatalf("state = %v", got)
	}
	if got := m["generation"].Value(); got != int64(7) {
		t.Fatalf("generation = %v", got)
	}
	if got := m["progress_total"].Value(); got != uint32(10) {
		t.Fatalf("progress_total = %v", got)
	}
	if got := m["paused"].Value(); got != true {
		t.Fatalf("paused = %v", got)
	}
	if got := m["free_bytes"].Value(); got != int64(1<<30) {
		t.Fatalf("free_bytes = %v", got)
	}
}

func TestEmitWithoutConnectionIsSafe(t *testing.T) {
	s := &Service{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	s.StateChanged(daemon.StateIdle)
	s.Progress(1, 2, 3)
	s.FileDone("a", "push", "ok")
	if err := s.Close(); err != nil {
		t.Fatalf("Close = %v", err)
	}
}

func TestIntrospectionMatchesService(t *testing.T) {
	var node introspect.Node
	if err := xml.Unmarshal([]byte(walky1XML), &node); err != nil {
		t.Fatalf("parse walky1.xml: %v", err)
	}
	if len(node.Interfaces) != 1 || node.Interfaces[0].Name != Interface {
		t.Fatalf("interfaces = %+v", node.Interfaces)
	}
	xmlMethods := map[string]bool{}
	for _, m := range node.Interfaces[0].Methods {
		xmlMethods[m.Name] = true
	}

	dbusErrType := reflect.TypeOf((*dbus.Error)(nil))
	svcType := reflect.TypeOf(&Service{})
	for i := 0; i < svcType.NumMethod(); i++ {
		m := svcType.Method(i)
		mt := m.Type
		if mt.NumOut() == 0 || mt.Out(mt.NumOut()-1) != dbusErrType {
			continue
		}
		if !xmlMethods[m.Name] {
			t.Errorf("D-Bus method %s is missing from walky1.xml", m.Name)
		}
		delete(xmlMethods, m.Name)
	}
	for name := range xmlMethods {
		t.Errorf("walky1.xml declares method %s that does not exist on *Service", name)
	}
}
