package events

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBrokerDeliversOrderedEventsWithoutBlockingOnSlowSubscriber(t *testing.T) {
	broker := NewBroker(1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fast := broker.Subscribe(ctx)
	slow := broker.Subscribe(ctx)

	broker.Publish(Event{Type: "operation.updated", Data: json.RawMessage(`{"operation_id":"operation-a"}`)})
	first := <-fast
	require.Equal(t, uint64(1), first.ID)
	require.Equal(t, "operation.updated", first.Type)
	broker.Publish(Event{Type: "server.updated", Data: json.RawMessage(`{"server_id":"server-a"}`)})
	second := <-fast
	require.Equal(t, uint64(2), second.ID)
	require.Equal(t, "server.updated", second.Type)

	_, open := <-slow
	require.True(t, open)
	_, open = <-slow
	require.False(t, open)
}

func TestBrokerClosesSubscriptionWhenContextEnds(t *testing.T) {
	broker := NewBroker(2)
	ctx, cancel := context.WithCancel(context.Background())
	stream := broker.Subscribe(ctx)
	cancel()
	_, open := <-stream
	require.False(t, open)
}
