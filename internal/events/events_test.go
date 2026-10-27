package events

import (
	"sync"
	"testing"
)

func ids(events []Event) []int64 {
	out := []int64{}
	for _, e := range events {
		out = append(out, e.ID)
	}
	return out
}

func TestRingBuffer(t *testing.T) {
	b := NewBroker(3)
	if len(b.Recent()) != 0 {
		t.Fatal("not empty")
	}
	for range 2 {
		b.Publish(Event{})
	}
	if got := ids(b.Recent()); len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("recent = %v", got)
	}
	for range 4 {
		b.Publish(Event{})
	}
	if got := ids(b.Recent()); len(got) != 3 || got[0] != 4 || got[2] != 6 {
		t.Fatalf("recent after wrap = %v", got)
	}
}

func TestZeroSizeBroker(t *testing.T) {
	b := NewBroker(0)
	e := b.Publish(Event{Op: "x"})
	if e.ID != 1 || len(b.Recent()) != 0 {
		t.Fatal("zero-size broker should still assign IDs and keep nothing")
	}
}

func TestSubscribe(t *testing.T) {
	b := NewBroker(10)
	b.Publish(Event{Op: "before"})

	recent, ch, cancel := b.Subscribe()
	if len(recent) != 1 || recent[0].Op != "before" {
		t.Fatalf("backlog = %+v", recent)
	}
	b.Publish(Event{Op: "after"})
	if e := <-ch; e.Op != "after" || e.ID != 2 {
		t.Fatalf("live event = %+v", e)
	}

	cancel()
	cancel() // idempotent
	if _, ok := <-ch; ok {
		t.Fatal("channel not closed")
	}
	b.Publish(Event{}) // must not panic or block after cancel
}

func TestSlowSubscriberDoesNotBlock(t *testing.T) {
	b := NewBroker(1)
	_, _, cancel := b.Subscribe()
	defer cancel()
	for range subscriberBuffer * 2 {
		b.Publish(Event{})
	}
}

func TestConcurrentPublish(t *testing.T) {
	b := NewBroker(50)
	_, ch, cancel := b.Subscribe()
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			for range 20 {
				b.Publish(Event{})
			}
		})
	}
	wg.Wait()
	cancel()
	seen := map[int64]bool{}
	for e := range ch {
		if seen[e.ID] {
			t.Fatalf("duplicate id %d", e.ID)
		}
		seen[e.ID] = true
	}
	if len(seen) != 200 {
		t.Fatalf("got %d events, want 200", len(seen))
	}
}
