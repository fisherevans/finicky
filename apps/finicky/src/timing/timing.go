// Package timing records bounded local metadata without URL paths or error text.
package timing

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

const MaxBytes = 2 * 1024 * 1024
const Backups = 4

type Event struct {
	Opener         string    `json:"opener_app,omitempty"`
	Time           time.Time `json:"time"`
	Session        string    `json:"session"`
	Kind           string    `json:"event"`
	ID             uint64    `json:"request_id,omitempty"`
	Host           string    `json:"hostname,omitempty"`
	App            string    `json:"app,omitempty"`
	Profile        string    `json:"profile,omitempty"`
	Foreground     string    `json:"foreground_app,omitempty"`
	DurationMS     float64   `json:"duration_ms,omitempty"`
	SinceReceiptMS float64   `json:"since_receipt_ms,omitempty"`
	IdleMS         *float64  `json:"previous_receipt_ms,omitempty"`
	Error          string    `json:"error_class,omitempty"`
	Background     bool      `json:"background,omitempty"`
	DryRun         bool      `json:"dry_run,omitempty"`
	Dropped        uint64    `json:"dropped_events,omitempty"`
}

type Recorder struct {
	queue        chan Event
	done         chan struct{}
	session      string
	seq          atomic.Uint64
	previous     atomic.Int64
	observeUntil atomic.Int64
	dropped      atomic.Uint64
	foreground   atomic.Value
}

type Trace struct {
	recorder *Recorder
	ID       uint64
	Received time.Time
}

func New(dir string) *Recorder {
	r := &Recorder{queue: make(chan Event, 256), done: make(chan struct{}), session: fmt.Sprintf("%d-%d", time.Now().UnixNano(), os.Getpid())}
	go r.write(dir)
	return r
}

func Host(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || len(u.Hostname()) > 253 {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

func label(s string) string {
	if len(s) > 128 {
		return "redacted"
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune(" ._-()", c)) {
			return "redacted"
		}
	}
	return s
}

func (r *Recorder) Record(e Event) {
	if r == nil {
		return
	}
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	e.Session = r.session
	e.App, e.Profile, e.Foreground, e.Opener = label(e.App), label(e.Profile), label(e.Foreground), label(e.Opener)
	select {
	case r.queue <- e:
	default:
		r.dropped.Add(1)
	}
}

func (r *Recorder) Foreground(app string) {
	if r == nil {
		return
	}
	r.foreground.Store(label(app))
	now := time.Now()
	if now.UnixNano() <= r.observeUntil.Load() {
		// Foreground events have no request ID: overlapping opens prevent causal attribution.
		r.Record(Event{Time: now, Kind: "foreground_observed", App: app})
	}
}

func (r *Recorder) Begin(raw string, received time.Time, opener string) *Trace {
	if r == nil {
		return nil
	}
	t := &Trace{recorder: r, ID: r.seq.Add(1), Received: received}
	e := Event{Time: received, Kind: "received", ID: t.ID, Host: Host(raw), Opener: opener}
	if prior := r.previous.Swap(received.UnixNano()); prior != 0 {
		idle := float64(received.UnixNano()-prior) / 1e6
		e.IdleMS = &idle
	}
	if app, ok := r.foreground.Load().(string); ok {
		e.Foreground = app
	}
	r.observeUntil.Store(received.Add(15 * time.Second).UnixNano())
	r.Record(e)
	return t
}

func (t *Trace) Record(e Event) {
	if t == nil {
		return
	}
	now := time.Now()
	e.Time, e.ID, e.SinceReceiptMS = now, t.ID, float64(now.Sub(t.Received))/float64(time.Millisecond)
	t.recorder.Record(e)
}

func (t *Trace) Stage(kind string, start time.Time, failed bool) {
	e := Event{Kind: kind, DurationMS: float64(time.Since(start)) / float64(time.Millisecond)}
	if failed {
		e.Error = kind + "_failed"
	}
	t.Record(e)
}

// Close is for controlled shutdowns after producers stop. Sudden exit may lose queued events.
func (r *Recorder) Close() { close(r.queue); <-r.done }

func (r *Recorder) write(dir string) {
	defer close(r.done)
	var file *os.File
	var size int64
	defer func() {
		if file != nil {
			file.Close()
		}
	}()
	path := filepath.Join(dir, "events.jsonl")
	for e := range r.queue {
		e.Dropped = r.dropped.Swap(0)
		data, err := json.Marshal(e)
		if err != nil {
			r.dropped.Add(e.Dropped + 1)
			continue
		}
		data = append(data, '\n')
		if file != nil && size+int64(len(data)) > MaxBytes {
			file.Close()
			file = nil
			// Rotation and disk errors never block the URL handler.
			for i := Backups - 1; i >= 1; i-- {
				if err := os.Rename(fmt.Sprintf("%s.%d", path, i), fmt.Sprintf("%s.%d", path, i+1)); err != nil && !os.IsNotExist(err) {
					break
				}
			}
			if err := os.Rename(path, path+".1"); err != nil {
				r.dropped.Add(e.Dropped + 1)
				continue
			}
		}
		if file == nil {
			if err := os.MkdirAll(dir, 0700); err != nil {
				r.dropped.Add(e.Dropped + 1)
				continue
			}
			file, err = os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
			if err != nil {
				r.dropped.Add(e.Dropped + 1)
				continue
			}
			if err := file.Chmod(0600); err != nil {
				file.Close()
				file = nil
				r.dropped.Add(e.Dropped + 1)
				continue
			}
			info, err := file.Stat()
			if err != nil {
				file.Close()
				file = nil
				r.dropped.Add(e.Dropped + 1)
				continue
			}
			size = info.Size()
			if size+int64(len(data)) > MaxBytes {
				// Requeue is unnecessary; the next event rotates an existing full file.
				r.dropped.Add(e.Dropped + 1)
				continue
			}
		}
		n, err := file.Write(data)
		size += int64(n)
		if err != nil {
			file.Close()
			file = nil
			r.dropped.Add(e.Dropped + 1)
		}
	}
}
