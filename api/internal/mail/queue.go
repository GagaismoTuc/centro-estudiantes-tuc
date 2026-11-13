package mail

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
)

// Queue runs background jobs (mail deliveries) on a fixed number of workers with a bounded backlog,
// so a burst of requests cannot start unbounded goroutines.
//
// shortcut: the backlog lives in memory; queued messages are lost on a crash or when the backlog is
// full (counted by Dropped). A persistent outbox replaces it when mail volume needs it.
type Queue struct {
	mu      sync.RWMutex
	closed  bool
	jobs    chan func()
	wg      sync.WaitGroup
	dropped atomic.Uint64
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

// Go queues fn without blocking. When the backlog is full, or the queue is closed, fn is dropped:
// the drop is counted and logged at WARN with the running total (the job itself is never logged: it
// may carry a token).
func (q *Queue) Go(fn func()) {
	q.mu.RLock()
	defer q.mu.RUnlock()
	if q.closed {
		slog.Warn("mail queue closed: message dropped", "dropped", q.dropped.Add(1))
		return
	}
	select {
	case q.jobs <- fn:
	default:
		slog.Warn("mail queue full: message dropped", "dropped", q.dropped.Add(1))
	}
}

// Dropped returns how many jobs were dropped since the queue started.
func (q *Queue) Dropped() uint64 { return q.dropped.Load() }

// Close stops accepting jobs and waits until every queued job has run, or until ctx is done. It may
// be called more than once, also concurrently.
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
