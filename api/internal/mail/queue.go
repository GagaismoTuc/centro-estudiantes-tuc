package mail

import (
	"context"
	"log/slog"
	"sync"
)

// Queue runs background jobs (mail deliveries) on a fixed number of workers with a bounded backlog,
// so a burst of requests cannot start unbounded goroutines.
//
// shortcut: the backlog lives in memory; queued messages are lost on a crash (a persistent outbox
// is a T7b follow-up).
type Queue struct {
	mu     sync.RWMutex
	closed bool
	jobs   chan func()
	wg     sync.WaitGroup
}

// NewQueue starts workers goroutines sharing a backlog of size jobs.
func NewQueue(workers, size int) *Queue {
	q := &Queue{jobs: make(chan func(), size)}
	q.wg.Add(workers)
	for range workers {
		go func() {
			defer q.wg.Done()
			for fn := range q.jobs {
				fn()
			}
		}()
	}
	return q
}

// Go queues fn without blocking. When the backlog is full, or the queue is closed, fn is dropped and
// the drop is logged (the job itself is never logged: it may carry a token).
func (q *Queue) Go(fn func()) {
	q.mu.RLock()
	defer q.mu.RUnlock()
	if q.closed {
		slog.Error("mail queue closed: message dropped")
		return
	}
	select {
	case q.jobs <- fn:
	default:
		slog.Error("mail queue full: message dropped")
	}
}

// Close stops accepting jobs and waits until every queued job has run, or until ctx is done.
func (q *Queue) Close(ctx context.Context) error {
	q.mu.Lock()
	if !q.closed {
		q.closed = true
		close(q.jobs)
	}
	q.mu.Unlock()
	done := make(chan struct{})
	go func() { q.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
