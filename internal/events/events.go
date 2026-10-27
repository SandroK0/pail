// Package events keeps a ring buffer of recent S3 requests and fans new ones
// out to live subscribers (the UI's request log).
package events

import (
	"sync"
	"time"
)

type Event struct {
	ID           int64     `json:"id"`
	Time         time.Time `json:"time"`
	Op           string    `json:"op"`
	Method       string    `json:"method"`
	Path         string    `json:"path"`
	Bucket       string    `json:"bucket,omitempty"`
	Key          string    `json:"key,omitempty"`
	Status       int       `json:"status"`
	ErrorCode    string    `json:"errorCode,omitempty"`
	ErrorMessage string    `json:"errorMessage,omitempty"`
	BytesIn      int64     `json:"bytesIn"`
	BytesOut     int64     `json:"bytesOut"`
	DurationMs   float64   `json:"durationMs"`
	AccessKey    string    `json:"accessKey,omitempty"`
	Presigned    bool      `json:"presigned,omitempty"`
	UserAgent    string    `json:"userAgent,omitempty"`
	RequestID    string    `json:"requestId,omitempty"` // also sent as x-amz-request-id
}

const subscriberBuffer = 256

type Broker struct {
	mu     sync.Mutex
	buf    []Event
	start  int // index of the oldest event once buf is full
	nextID int64
	subs   map[chan Event]struct{}
}

func NewBroker(size int) *Broker {
	return &Broker{buf: make([]Event, 0, size), subs: map[chan Event]struct{}{}}
}

// Publish assigns e an ID, records it and sends it to subscribers. A
// subscriber that has fallen behind misses events rather than blocking.
func (b *Broker) Publish(e Event) Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.nextID++
	e.ID = b.nextID
	if len(b.buf) < cap(b.buf) {
		b.buf = append(b.buf, e)
	} else if cap(b.buf) > 0 {
		b.buf[b.start] = e
		b.start = (b.start + 1) % cap(b.buf)
	}
	for ch := range b.subs {
		select {
		case ch <- e:
		default:
		}
	}
	return e
}

// Recent returns buffered events, oldest first.
func (b *Broker) Recent() []Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.recentLocked()
}

func (b *Broker) recentLocked() []Event {
	out := make([]Event, 0, len(b.buf))
	out = append(out, b.buf[b.start:]...)
	return append(out, b.buf[:b.start]...)
}

// Subscribe returns the buffered events and a channel of new ones, taken
// atomically so nothing is missed or repeated. Call cancel when done.
func (b *Broker) Subscribe() (recent []Event, ch <-chan Event, cancel func()) {
	c := make(chan Event, subscriberBuffer)
	b.mu.Lock()
	recent = b.recentLocked()
	b.subs[c] = struct{}{}
	b.mu.Unlock()
	return recent, c, func() {
		b.mu.Lock()
		if _, ok := b.subs[c]; ok {
			delete(b.subs, c)
			close(c)
		}
		b.mu.Unlock()
	}
}
