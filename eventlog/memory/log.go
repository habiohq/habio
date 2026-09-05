// Package memory provides a small local EventSink for examples and tests. It is
// not a durable event-store commitment.
package memory

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/habiohq/habio"
)

// ErrEventConflict means an existing EventID was reused for different facts.
var ErrEventConflict = errors.New("habio memory event log: event ID conflict")

// Log retains immutable events in append order.
type Log struct {
	mu     sync.RWMutex
	events []habio.ExecutionEvent
	byID   map[habio.EventID]habio.ExecutionEvent
}

// New returns an empty memory Log.
func New() *Log {
	return &Log{byID: make(map[habio.EventID]habio.ExecutionEvent)}
}

// Append adds event, treats an identical EventID and fact as an idempotent
// duplicate, and rejects conflicting reuse of an EventID.
func (l *Log) Append(ctx context.Context, event habio.ExecutionEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.byID == nil {
		l.byID = make(map[habio.EventID]habio.ExecutionEvent)
	}
	if existing, ok := l.byID[event.ID()]; ok {
		if equal(existing, event) {
			return nil
		}
		return fmt.Errorf("%w: %s", ErrEventConflict, event.ID())
	}
	l.byID[event.ID()] = event
	l.events = append(l.events, event)
	return nil
}

// Events returns a copy of events in canonical append order.
func (l *Log) Events() []habio.ExecutionEvent {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return append([]habio.ExecutionEvent(nil), l.events...)
}

func equal(a, b habio.ExecutionEvent) bool {
	return a.ID() == b.ID() &&
		a.ActionID() == b.ActionID() &&
		a.AttemptID() == b.AttemptID() &&
		a.Kind() == b.Kind() &&
		a.OccurredAt().Equal(b.OccurredAt()) &&
		a.RecordedAt().Equal(b.RecordedAt()) &&
		bytes.Equal(a.Data(), b.Data())
}

var _ habio.EventSink = (*Log)(nil)
