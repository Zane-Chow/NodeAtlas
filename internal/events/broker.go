package events

import (
	"context"
	"encoding/json"
	"sync"
)

type Event struct {
	ID   uint64          `json:"id"`
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

type Publisher interface {
	Publish(Event)
}

type Subscriber interface {
	Subscribe(context.Context) <-chan Event
}

type Broker struct {
	mutex       sync.Mutex
	bufferSize  int
	nextEventID uint64
	nextClient  uint64
	clients     map[uint64]chan Event
}

func NewBroker(bufferSize int) *Broker {
	if bufferSize < 1 {
		bufferSize = 16
	}
	return &Broker{bufferSize: bufferSize, clients: make(map[uint64]chan Event)}
}

func (broker *Broker) Publish(event Event) {
	broker.mutex.Lock()
	defer broker.mutex.Unlock()
	broker.nextEventID++
	event.ID = broker.nextEventID
	for id, stream := range broker.clients {
		select {
		case stream <- event:
		default:
			close(stream)
			delete(broker.clients, id)
		}
	}
}

func (broker *Broker) Subscribe(ctx context.Context) <-chan Event {
	broker.mutex.Lock()
	broker.nextClient++
	id := broker.nextClient
	stream := make(chan Event, broker.bufferSize)
	broker.clients[id] = stream
	broker.mutex.Unlock()
	go func() {
		<-ctx.Done()
		broker.mutex.Lock()
		if current, exists := broker.clients[id]; exists {
			close(current)
			delete(broker.clients, id)
		}
		broker.mutex.Unlock()
	}()
	return stream
}
