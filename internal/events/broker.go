// Package events fans out data.Events to subscribers within one process,
// such as the server-sent event streams connected to it.
package events

import (
	"sync"

	"menugo.flayshon.com/internal/data"
)

// Subscription receives the events its filter matches on C. C is closed
// when the subscription ends: on Unsubscribe, or because the subscriber fell
// too far behind (see Broker.Publish).
type Subscription struct {
	C     <-chan data.Event
	c     chan data.Event
	match func(data.Event) bool
}

// Broker delivers published events to matching subscriptions.
type Broker struct {
	mu   sync.Mutex
	subs map[*Subscription]struct{}
}

func NewBroker() *Broker {
	return &Broker{subs: make(map[*Subscription]struct{})}
}

// Subscribe starts receiving events for which match returns true. buffer is
// how many events may wait for the subscriber before it is dropped.
func (b *Broker) Subscribe(match func(data.Event) bool, buffer int) *Subscription {
	c := make(chan data.Event, buffer)
	s := &Subscription{C: c, c: c, match: match}

	b.mu.Lock()
	b.subs[s] = struct{}{}
	b.mu.Unlock()
	return s
}

// Unsubscribe ends s. It is safe to call more than once.
func (b *Broker) Unsubscribe(s *Subscription) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.remove(s)
}

func (b *Broker) remove(s *Subscription) {
	if _, ok := b.subs[s]; ok {
		delete(b.subs, s)
		close(s.c)
	}
}

// Publish delivers e to every matching subscription without waiting. A
// subscriber whose buffer is full is dropped rather than slowing everyone
// else down; its channel is closed, so it can reconnect and catch up.
func (b *Broker) Publish(e data.Event) {
	b.mu.Lock()
	defer b.mu.Unlock()

	for s := range b.subs {
		if !s.match(e) {
			continue
		}
		select {
		case s.c <- e:
		default:
			b.remove(s)
		}
	}
}

// Len returns the number of subscriptions.
func (b *Broker) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subs)
}
