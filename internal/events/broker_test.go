package events

import (
	"testing"

	"menugo.flayshon.com/internal/data"
)

func TestBroker(t *testing.T) {
	b := NewBroker()

	restaurant1 := b.Subscribe(func(e data.Event) bool { return e.RestaurantID == 1 }, 10)
	order7 := b.Subscribe(func(e data.Event) bool { return e.OrderID == 7 }, 10)

	b.Publish(data.Event{ID: 1, RestaurantID: 1, OrderID: 7})
	b.Publish(data.Event{ID: 2, RestaurantID: 1, OrderID: 8})
	b.Publish(data.Event{ID: 3, RestaurantID: 2, OrderID: 9})

	if got := drain(restaurant1); len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Errorf("restaurant 1 got %v; want [1 2]", got)
	}
	if got := drain(order7); len(got) != 1 || got[0] != 1 {
		t.Errorf("order 7 got %v; want [1]", got)
	}

	b.Unsubscribe(order7)
	b.Unsubscribe(order7) // twice is fine
	if _, open := <-order7.C; open {
		t.Error("channel should be closed after Unsubscribe")
	}
	if b.Len() != 1 {
		t.Errorf("Len = %d; want 1", b.Len())
	}
}

func TestSlowSubscribersAreDropped(t *testing.T) {
	b := NewBroker()
	all := func(data.Event) bool { return true }

	slow := b.Subscribe(all, 2)
	fast := b.Subscribe(all, 10)

	for i := range 3 {
		b.Publish(data.Event{ID: int64(i + 1)})
	}

	// The slow one got what fit, then its channel was closed.
	if got := drain(slow); len(got) != 2 {
		t.Errorf("slow got %v; want 2 events", got)
	}
	if _, open := <-slow.C; open {
		t.Error("slow subscriber should have been dropped")
	}
	if got := drain(fast); len(got) != 3 {
		t.Errorf("fast got %v; want all 3", got)
	}
	if b.Len() != 1 {
		t.Errorf("Len = %d; want 1", b.Len())
	}
}

// drain returns the IDs of the events waiting in s without blocking.
func drain(s *Subscription) []int64 {
	var ids []int64
	for {
		select {
		case e, ok := <-s.C:
			if !ok {
				return ids
			}
			ids = append(ids, e.ID)
		default:
			return ids
		}
	}
}
