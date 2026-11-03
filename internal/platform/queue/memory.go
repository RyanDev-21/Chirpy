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
// Jobs are lost on restart, so it is meant for tests and local runs.
type Memory struct {
	mu         sync.Mutex
	topics     map[string]chan memJob
	maxRetries int
	logger     *slog.Logger
	wg         sync.WaitGroup
}

type memJob struct {
	payload json.RawMessage
	attempt int
}

func NewMemory(maxRetries int, logger *slog.Logger) *Memory {
	return &Memory{topics: make(map[string]chan memJob), maxRetries: maxRetries, logger: logger}
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

// Backoff returns the delay before retry attempt n (1-based).
func Backoff(n int) time.Duration {
	d := 200 * time.Millisecond << min(n-1, 8)
	return min(d, 30*time.Second)
}
