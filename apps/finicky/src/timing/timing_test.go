package timing

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPrivacyAndStages(t *testing.T) {
	dir := t.TempDir()
	r := New(dir)
	trace := r.Begin("https://user:SECRET@example.com/private/TOKEN?auth=SECRET#SECRET", time.Now(), "com.example.sender")
	trace.Record(Event{Kind: "dispatch_started", App: "https://SECRET/", Profile: "SECRET?token=value"})
	trace.Stage("resolution", time.Now(), true)
	r.Begin("https://example.net", time.Now(), "com.example.sender")
	r.Close()
	data, err := os.ReadFile(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"SECRET", "TOKEN", "private", "auth", "user:"} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("sensitive value %q persisted", secret)
		}
	}
	if !strings.Contains(string(data), "example.com") || !strings.Contains(string(data), "previous_receipt_ms") {
		t.Fatal("missing metadata")
	}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var e Event
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatal(err)
		}
		if e.Session == "" || e.Time.IsZero() {
			t.Fatal("missing event identity")
		}
	}
	info, _ := os.Stat(filepath.Join(dir, "events.jsonl"))
	if info.Mode().Perm() != 0600 {
		t.Fatal("metrics are not private")
	}
}

func TestQueueDoesNotBlock(t *testing.T) {
	r := &Recorder{queue: make(chan Event, 1)}
	for i := 0; i < 10000; i++ {
		r.Record(Event{Kind: "test"})
	}
	if r.dropped.Load() != 9999 {
		t.Fatal("overflow not counted")
	}
}

func TestRotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")
	for i := 0; i < Backups+2; i++ {
		if err := os.WriteFile(path, []byte(strings.Repeat("x", MaxBytes)), 0600); err != nil {
			t.Fatal(err)
		}
		r := New(dir)
		r.Record(Event{Kind: "first"})
		r.Record(Event{Kind: "second"})
		r.Close()
	}
	files, _ := os.ReadDir(dir)
	if len(files) != Backups+1 {
		t.Fatalf("got %d files", len(files))
	}
	for _, f := range files {
		info, _ := f.Info()
		if info.Size() > MaxBytes {
			t.Fatal("file exceeds bound")
		}
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "dropped_events") {
		t.Fatal("missing dropped count")
	}
}

func TestForegroundWindowAndNilTrace(t *testing.T) {
	var trace *Trace
	trace.Stage("test", time.Now(), false)
	dir := t.TempDir()
	r := New(dir)
	r.Foreground("before")
	r.Begin("file:///private/SECRET", time.Now(), "com.example.sender")
	r.Foreground("during")
	r.observeUntil.Store(0)
	r.Foreground("after")
	r.Close()
	data, _ := os.ReadFile(filepath.Join(dir, "events.jsonl"))
	if strings.Count(string(data), "foreground_observed") != 1 || strings.Contains(string(data), "SECRET") {
		t.Fatal(string(data))
	}
}

func TestInvalidURLDoesNotLeak(t *testing.T) {
	for _, raw := range []string{"https://example.com/%SECRET", "file:///SECRET", "spotify:SECRET", "https://user:SECRET@/path"} {
		if h := Host(raw); h != "" {
			t.Fatalf("unexpected host %q", h)
		}
	}
}

func BenchmarkRecord(b *testing.B) {
	r := &Recorder{queue: make(chan Event, 256)}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		r.Record(Event{Kind: "received", Host: "example.com"})
	}
}

func TestConcurrentProducers(t *testing.T) {
	r := New(t.TempDir())
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				trace := r.Begin("https://example.com", time.Now(), "com.example.sender")
				r.Foreground("org.mozilla.firefox")
				trace.Stage("dispatch_completed", time.Now(), false)
			}
		}()
	}
	wg.Wait()
	r.Close()
}

func TestDiskFailureDoesNotBlock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	r := New(filepath.Join(path, "impossible"))
	for i := 0; i < 1000; i++ {
		r.Record(Event{Kind: "test"})
	}
	r.Close()
	if r.dropped.Load() != 1000 {
		t.Fatal("disk failures were not counted")
	}
}
