// Package device discovers mounted Walky drives by their marker file.
package device

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"walky/internal/store"
)

// Mount is one entry of the mount table.
type Mount struct {
	Point  string
	FSType string
	Source string
}

// Device is a mounted Walky store.
type Device struct {
	MountPoint string
	StoreRoot  string
	Label      string
	Marker     store.Marker
}

// Key identifies the store across attach/detach cycles.
func (d Device) Key() string { return d.StoreRoot }

// EventKind is an attach or detach notification.
type EventKind int

const (
	// Attached means a Walky store became visible.
	Attached EventKind = iota
	// Detached means the store disappeared.
	Detached
)

// Event describes a device state change.
type Event struct {
	Kind   EventKind
	Device Device
}

// ReadMounts parses a mountinfo file (normally /proc/self/mountinfo).
func ReadMounts(path string) ([]Mount, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var out []Mount
	seen := make(map[string]struct{})
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 7 {
			continue
		}
		sep := -1
		for i, f := range fields {
			if f == "-" {
				sep = i
				break
			}
		}
		if sep < 0 || sep+2 >= len(fields) {
			continue
		}
		point := unescapeMount(fields[4])
		if _, ok := seen[point]; ok {
			continue
		}
		seen[point] = struct{}{}
		out = append(out, Mount{Point: point, FSType: fields[sep+1], Source: fields[sep+2]})
	}
	return out, sc.Err()
}

// unescapeMount decodes the octal escapes used by mountinfo (space, tab, ...).
func unescapeMount(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if v, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(v))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// Watcher polls the mount table and reports Walky stores.
type Watcher struct {
	interval time.Duration
	mounts   func() ([]Mount, error)
}

// NewWatcher returns a watcher over /proc/self/mountinfo.
func NewWatcher(interval time.Duration) *Watcher {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	return NewWatcherWithSource(interval, func() ([]Mount, error) {
		return ReadMounts("/proc/self/mountinfo")
	})
}

// NewWatcherWithSource lets tests inject a mount source.
func NewWatcherWithSource(interval time.Duration, source func() ([]Mount, error)) *Watcher {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	return &Watcher{interval: interval, mounts: source}
}

// Scan returns the currently mounted Walky stores.
func (w *Watcher) Scan() ([]Device, error) {
	mounts, err := w.mounts()
	if err != nil {
		return nil, err
	}
	var out []Device
	for _, m := range mounts {
		candidates := []string{filepath.Join(m.Point, "WALKY")}
		if filepath.Base(m.Point) != "WALKY" {
			candidates = append(candidates, m.Point)
		}
		for _, root := range candidates {
			marker, err := store.ReadMarker(root)
			if err != nil {
				continue
			}
			label := marker.Label
			if label == "" {
				label = filepath.Base(root)
			}
			out = append(out, Device{
				MountPoint: m.Point,
				StoreRoot:  root,
				Label:      label,
				Marker:     marker,
			})
			break
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StoreRoot < out[j].StoreRoot })
	return out, nil
}

// Run polls the mount table until ctx is cancelled and emits attach/detach
// events. The initial scan emits Attached for stores that are already mounted.
func (w *Watcher) Run(ctx context.Context, out chan<- Event) error {
	seen := make(map[string]Device)
	check := func() error {
		devices, err := w.Scan()
		if err != nil {
			return err
		}
		current := make(map[string]Device, len(devices))
		for _, d := range devices {
			current[d.Key()] = d
		}
		for key, d := range current {
			if _, ok := seen[key]; !ok {
				if !send(ctx, out, Event{Kind: Attached, Device: d}) {
					return nil
				}
			}
		}
		for key, d := range seen {
			if _, ok := current[key]; !ok {
				if !send(ctx, out, Event{Kind: Detached, Device: d}) {
					return nil
				}
			}
		}
		seen = current
		return nil
	}

	if err := check(); err != nil {
		return err
	}
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := check(); err != nil {
				return err
			}
		}
	}
}

func send(ctx context.Context, out chan<- Event, ev Event) bool {
	select {
	case out <- ev:
		return true
	case <-ctx.Done():
		return false
	}
}

// Describe renders a device for logs and status output.
func (d Device) Describe() string {
	return fmt.Sprintf("%s (%s)", d.StoreRoot, d.Marker.RepoID)
}
