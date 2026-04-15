package api

import (
	"encoding/json"
	"sync"
)

type EventBus struct {
	mu          sync.RWMutex
	subscribers map[string]chan Event
}

type Event struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

func NewEventBus() *EventBus {
	return &EventBus{
		subscribers: make(map[string]chan Event),
	}
}

func (eb *EventBus) Subscribe(id string) chan Event {
	eb.mu.Lock()
	defer eb.mu.Unlock()
	ch := make(chan Event, 32)
	eb.subscribers[id] = ch
	return ch
}

func (eb *EventBus) Unsubscribe(id string) {
	eb.mu.Lock()
	defer eb.mu.Unlock()
	if ch, ok := eb.subscribers[id]; ok {
		close(ch)
		delete(eb.subscribers, id)
	}
}

func (eb *EventBus) Publish(event Event) {
	eb.mu.RLock()
	defer eb.mu.RUnlock()
	for _, ch := range eb.subscribers {
		select {
		case ch <- event:
		default:
			// drop if subscriber is slow
		}
	}
}

func (e Event) SSEFormat() string {
	data, _ := json.Marshal(e.Data)
	return "event: " + e.Type + "\ndata: " + string(data) + "\n\n"
}
