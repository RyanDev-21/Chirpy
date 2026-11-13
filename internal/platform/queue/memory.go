package queue

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"
)

// Memory is an in-process queue with the same semantics as the Redis
// queue (JSON payloads, retries with backoff, permanent errors dropped).
// Jobs still waiting at shutdown are run by Drain; a crash loses them.
type Memory struct {
	mu         sync.Mutex
	topics     map[string]chan memJob
	handlers   map[string]Handler
	maxRetries int
	logger     *slog.Logger
	wg         sync.WaitGroup
}

type memJob struct {
	payload json.RawMessage
	attempt int
}

func NewMemory(maxRetries int, logger *slog.Logger) *Memory {
	return &Memory{topics: make(map[string]chan memJob), handlers: make(map[string]Handler), maxRetries: maxRetries, logger: logger}
}

func (m *Memory) topic(name string) chan memJob {
	m.mu.Lock()
	defer m.mu.Unlock()
	ch, ok := m.topics[name]
	if !ok {
		ch = make(chan memJob, 1000)
		m.topics[name] = ch
	}
	return ch
}

func (m *Memory) Publish(ctx context.Context, topic string, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	select {
	case m.topic(topic) <- memJob{payload: raw}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Subscribe starts workers goroutines handling topic until ctx is done.
func (m *Memory) Subscribe(ctx context.Context, topic string, workers int, h Handler) error {
	ch := m.topic(topic)
	m.mu.Lock()
	m.handlers[topic] = h
	m.mu.Unlock()
	for range workers {
		m.wg.Add(1)
		go func() {
			defer m.wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case job := <-ch:
					m.handle(ctx, topic, ch, job, h)
				}
			}
		}()
	}
	return nil
}

func (m *Memory) handle(ctx context.Context, topic string, ch chan memJob, job memJob, h Handler) {
	err := h(ctx, job.payload)
	if err == nil {
		return
	}
	if IsPermanent(err) || job.attempt+1 >= m.maxRetries {
		m.logger.Error("job dropped", "topic", topic, "attempt", job.attempt+1, "error", err)
		return
	}
	job.attempt++
	time.AfterFunc(Backoff(job.attempt), func() {
		select {
		case ch <- job:
		default:
			m.logger.Error("retry dropped, topic full", "topic", topic)
		}
	})
}

// Wait blocks until all workers have exited (after ctx is cancelled).
func (m *Memory) Wait() { m.wg.Wait() }

// drainWorkers is how many jobs per topic Drain runs at once.
const drainWorkers = 4

// Drain runs the jobs still queued (once each, no retries) until the queues
// are empty or ctx ends, and returns how many were left undone. Call it
// after Wait, once nothing publishes any more (the HTTP server is shut
// down), so a deploy doesn't drop pushes and read markers.
func (m *Memory) Drain(ctx context.Context) int {
	m.mu.Lock()
	type pair struct {
		ch chan memJob
		h  Handler
	}
	var todo []pair
	for name, ch := range m.topics {
		if h := m.handlers[name]; h != nil {
			todo = append(todo, pair{ch, h})
		}
	}
	m.mu.Unlock()
	var wg sync.WaitGroup
	for _, p := range todo {
		for range drainWorkers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for ctx.Err() == nil {
					select {
					case job := <-p.ch:
						if err := p.h(ctx, job.payload); err != nil {
							m.logger.Warn("job failed during shutdown", "error", err)
						}
					default:
						return
					}
				}
			}()
		}
	}
	wg.Wait()
	left := 0
	for _, p := range todo {
		left += len(p.ch)
	}
	return left
}

// Backoff returns the delay before retry attempt n (1-based).
func Backoff(n int) time.Duration {
	d := 200 * time.Millisecond << min(n-1, 8)
	return min(d, 30*time.Second)
}
