// Package mq is a small in-process job queue. It is replaced by the Redis
// Streams queue in internal/platform/queue; this version only keeps the
// existing call sites working safely.
package mq

import (
	"context"
	"log"
	"sync"
	"time"
)

const topicBuffer = 1000

type WorkerFunction func(chan *Channel)

type Channel struct {
	topic        string
	LocalTag     int
	Msg          any
	RetriesCount int
}

type MainMQ struct {
	mu           sync.Mutex
	topics       map[string]chan *Channel
	tags         map[string]int
	retriesLimit int
}

func NewMainMQ(retriesLimit int) *MainMQ {
	return &MainMQ{
		topics:       make(map[string]chan *Channel),
		tags:         make(map[string]int),
		retriesLimit: retriesLimit,
	}
}

// topic returns the channel for name, creating it on first use.
func (mq *MainMQ) topic(name string) chan *Channel {
	mq.mu.Lock()
	defer mq.mu.Unlock()
	ch, ok := mq.topics[name]
	if !ok {
		ch = make(chan *Channel, topicBuffer)
		mq.topics[name] = ch
	}
	return ch
}

func (mq *MainMQ) nextTag(name string) int {
	mq.mu.Lock()
	defer mq.mu.Unlock()
	mq.tags[name]++
	return mq.tags[name]
}

// Run is kept for compatibility; the queue no longer needs a dispatcher loop.
func (mq *MainMQ) Run() {}

// PublishWithContext enqueues info on topic, giving up when ctx is done.
func (mq *MainMQ) PublishWithContext(ctx context.Context, topic string, info any) error {
	job := &Channel{topic: topic, LocalTag: mq.nextTag(topic), Msg: info}
	select {
	case mq.topic(topic) <- job:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Republish schedules a failed job for another attempt with exponential
// backoff. It never blocks the calling worker.
func (mq *MainMQ) Republish(channel *Channel, retries int) {
	if retries >= mq.retriesLimit {
		log.Printf("mq: job on %q (tag %d) dropped after %d retries", channel.topic, channel.LocalTag, retries)
		return
	}
	channel.RetriesCount = retries
	delay := min(time.Duration(1<<min(retries, 6))*100*time.Millisecond, 10*time.Second)
	time.AfterFunc(delay, func() {
		select {
		case mq.topic(channel.topic) <- channel:
		default:
			log.Printf("mq: topic %q is full, retry for tag %d dropped", channel.topic, channel.LocalTag)
		}
	})
}

// ListeningForTheChannels starts numWorkers workers consuming topic.
func (mq *MainMQ) ListeningForTheChannels(topic string, numWorkers int, workFunction WorkerFunction) {
	ch := mq.topic(topic)
	for range numWorkers {
		go workFunction(ch)
	}
}
