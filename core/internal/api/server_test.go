package api

import (
	"bufio"
	"encoding/xml"
	"errors"
	"io"
	"log/slog"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/introspect"

	"walky/internal/daemon"
)

func TestStatusMap(t *testing.T) {
	m := statusMap(daemon.Status{
		State:           daemon.StateIdle,
		Device:          "WALKY",
		StoreRoot:       "/run/media/u/WALKY/WALKY",
		RepoPath:        "/home/u/Sync",
		Generation:      7,
		LastSync:        1700000000,
		FreeBytes:       1 << 30,
		Encrypted:       true,
		EncryptionKnown: true,
		ProgressDone:    3,
		ProgressTotal:   10,
		Paused:          true,
	})
	if got := m["state"].Value(); got != daemon.StateIdle {
		t.Fatalf("state = %v", got)
	}
	if got := m["repo_path"].Value(); got != "/home/u/Sync" {
		t.Fatalf("repo_path = %v", got)
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
	if got := m["encrypted"].Value(); got != true {
		t.Fatalf("encrypted = %v", got)
	}
	if got := m["encryption_known"].Value(); got != true {
		t.Fatalf("encryption_known = %v", got)
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

func startPrivateBus(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath("dbus-daemon")
	if err != nil {
		t.Skip("dbus-daemon not installed")
	}
	cmd := exec.Command(bin, "--session", "--print-address=1", "--nofork")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start dbus-daemon: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		t.Fatalf("read bus address: %v", err)
	}
	return strings.TrimSpace(line)
}

func TestServiceOnPrivateBus(t *testing.T) {
	addr := startPrivateBus(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	d := daemon.New(daemon.Config{RepoPath: t.TempDir(), NoTray: true}, log)

	srvConn, err := dbus.Connect(addr)
	if err != nil {
		t.Fatalf("connect server: %v", err)
	}
	svc, err := NewWithConn(srvConn, d, log)
	if err != nil {
		t.Fatalf("NewWithConn: %v", err)
	}
	defer func() { _ = svc.Close() }()

	// A second instance on the same bus must be rejected.
	dupConn, err := dbus.Connect(addr)
	if err != nil {
		t.Fatalf("connect duplicate: %v", err)
	}
	if _, err := NewWithConn(dupConn, d, log); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("duplicate error = %v, want ErrAlreadyRunning", err)
	}

	cli, err := dbus.Connect(addr)
	if err != nil {
		t.Fatalf("connect client: %v", err)
	}
	defer func() { _ = cli.Close() }()
	obj := cli.Object(BusName, ObjectPath)

	var xmlData string
	if err := obj.Call("org.freedesktop.DBus.Introspectable.Introspect", 0).Store(&xmlData); err != nil {
		t.Fatalf("Introspect: %v", err)
	}
	if !strings.Contains(xmlData, Interface) {
		t.Fatalf("introspection XML missing %s", Interface)
	}

	if err := obj.Call(Interface+".Pause", 0, true).Err; err != nil {
		t.Fatalf("Pause(true): %v", err)
	}
	var st map[string]dbus.Variant
	if err := obj.Call(Interface+".GetStatus", 0).Store(&st); err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if paused, ok := st["paused"].Value().(bool); !ok || !paused {
		t.Fatalf("paused = %v, want true", st["paused"])
	}

	var events []daemon.Event
	if err := obj.Call(Interface+".GetEvents", 0, uint32(5)).Store(&events); err != nil {
		t.Fatalf("GetEvents: %v", err)
	}

	var settings map[string]dbus.Variant
	if err := obj.Call(Interface+".GetSettings", 0).Store(&settings); err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	if _, ok := settings["conflict"]; !ok {
		t.Fatalf("GetSettings missing conflict: %v", settings)
	}
}
