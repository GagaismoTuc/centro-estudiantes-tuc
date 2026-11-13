package mail_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/GagaismoTuc/centro-estudiantes-tuc/api/internal/mail"
)

func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &logs
}

// A full queue drops (and logs) instead of blocking the request; Close runs everything queued.
func TestQueueDropsWhenFullAndDrainsOnClose(t *testing.T) {
	logs := captureLogs(t)
	q := mail.NewQueue(1, 2)
	release := make(chan struct{})
	started := make(chan struct{})
	var ran atomic.Int32
	q.Go(func() { close(started); <-release; ran.Add(1) }) // occupies the only worker
	<-started
	q.Go(func() { ran.Add(1) })
	q.Go(func() { ran.Add(1) }) // queue now full (2)

	done := make(chan struct{})
	go func() { q.Go(func() { ran.Add(100) }); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Go blocked on a full queue")
	}
	if !strings.Contains(logs.String(), "mail queue full") {
		t.Fatalf("log = %q, want a dropped-message entry", logs.String())
	}

	closed := make(chan error)
	go func() { closed <- q.Close(context.Background()) }()
	close(release)
	if err := <-closed; err != nil {
		t.Fatalf("Close = %v", err)
	}
	if n := ran.Load(); n != 3 {
		t.Fatalf("jobs run = %d, want 3 (the dropped one never runs)", n)
	}
	q.Go(func() { ran.Add(100) }) // after Close: dropped, no panic
	if n := ran.Load(); n != 3 {
		t.Fatalf("job ran after Close")
	}
}

func TestQueueCloseGivesUpAtTheDeadline(t *testing.T) {
	q := mail.NewQueue(1, 1)
	release := make(chan struct{})
	defer close(release)
	q.Go(func() { <-release })
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := q.Close(ctx); err != context.DeadlineExceeded {
		t.Fatalf("Close = %v, want context.DeadlineExceeded", err)
	}
}
