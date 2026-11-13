package mail_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
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
	if !strings.Contains(logs.String(), `level=WARN msg="mail queue full: message dropped" dropped=1`) {
		t.Fatalf("log = %q, want a WARN dropped-message entry with the counter", logs.String())
	}
	if n := q.Dropped(); n != 1 {
		t.Fatalf("Dropped = %d, want 1", n)
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
	if n := q.Dropped(); n != 2 || !strings.Contains(logs.String(), `level=WARN msg="mail queue closed: message dropped" dropped=2`) {
		t.Fatalf("Dropped = %d, log = %q; want 2 and a WARN closed entry", n, logs.String())
	}
}

// Go racing with concurrent Close calls never panics (send on a closed channel) and every job is
// either run or counted as dropped. Run with -race.
func TestQueueGoRacesWithConcurrentClose(t *testing.T) {
	captureLogs(t)
	const senders, perSender = 16, 50
	q := mail.NewQueue(2, 8)
	var ran atomic.Int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range senders {
		wg.Go(func() {
			<-start
			for range perSender {
				q.Go(func() { ran.Add(1) })
			}
		})
	}
	closeErr := make(chan error, 2)
	for range 2 {
		wg.Go(func() { <-start; closeErr <- q.Close(context.Background()) })
	}
	close(start)
	wg.Wait()
	for range 2 {
		if err := <-closeErr; err != nil {
			t.Fatalf("Close = %v, want nil", err)
		}
	}
	if err := q.Close(context.Background()); err != nil {
		t.Fatalf("third Close = %v, want nil", err)
	}
	if got := uint64(ran.Load()) + q.Dropped(); got != senders*perSender {
		t.Fatalf("run %d + dropped %d = %d, want %d", ran.Load(), q.Dropped(), got, senders*perSender)
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
