package memory

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/habiohq/habio"
)

func TestAppendIsIdempotentAndAppendOnly(t *testing.T) {
	log := New()
	event := mustEvent(t, "event-1", habio.EventAttemptStarted)
	if err := log.Append(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if err := log.Append(context.Background(), event); err != nil {
		t.Fatalf("identical duplicate error = %v", err)
	}
	if got := len(log.Events()); got != 1 {
		t.Fatalf("len(Events()) = %d; want 1", got)
	}

	conflict, err := habio.NewExecutionEvent(habio.ExecutionEventSpec{
		ID: "event-1", ActionID: "action-1", AttemptID: "attempt-1",
		Kind: habio.EventActionDispatched, OccurredAt: event.OccurredAt(), RecordedAt: event.RecordedAt(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := log.Append(context.Background(), conflict); !errors.Is(err, ErrEventConflict) {
		t.Fatalf("conflicting duplicate error = %v; want ErrEventConflict", err)
	}
	if log.Events()[0].Kind() != habio.EventAttemptStarted {
		t.Fatal("conflicting duplicate replaced an append-only fact")
	}
}

func TestAppendPreservesCanonicalDeliveryOrder(t *testing.T) {
	log := New()
	laterOccurred := mustEvent(t, "event-later", habio.EventActionDispatched)
	earlierOccurred, err := habio.NewExecutionEvent(habio.ExecutionEventSpec{
		ID: "event-earlier", ActionID: "action-1", AttemptID: "attempt-1",
		Kind:       habio.EventActionAdmitted,
		OccurredAt: laterOccurred.OccurredAt().Add(-time.Minute), RecordedAt: laterOccurred.RecordedAt().Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := log.Append(context.Background(), laterOccurred); err != nil {
		t.Fatal(err)
	}
	if err := log.Append(context.Background(), earlierOccurred); err != nil {
		t.Fatal(err)
	}
	events := log.Events()
	if events[0].ID() != "event-later" || events[1].ID() != "event-earlier" {
		t.Fatal("Log reordered late facts instead of preserving append order")
	}
}

func mustEvent(t *testing.T, id habio.EventID, kind habio.EventKind) habio.ExecutionEvent {
	t.Helper()
	now := time.Now()
	event, err := habio.NewExecutionEvent(habio.ExecutionEventSpec{
		ID: id, ActionID: "action-1", AttemptID: "attempt-1", Kind: kind,
		OccurredAt: now, RecordedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	return event
}
