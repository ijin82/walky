package api

import (
	"io"
	"log/slog"
	"testing"

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
