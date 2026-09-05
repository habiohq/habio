// Package projection contains rebuildable views derived from Habio facts.
package projection

import (
	"errors"
	"fmt"

	"github.com/habiohq/habio"
)

var (
	ErrInvalidIdentity = errors.New("habio projection: invalid identity")
	ErrUnrelatedEvent  = errors.New("habio projection: unrelated event")
)

// Attempt is a rebuildable current view for one ExecutionAttempt.
type Attempt struct {
	actionID     habio.ActionID
	attemptID    habio.AttemptID
	admission    habio.AdmissionStatus
	dispatch     habio.DispatchStatus
	effect       habio.EffectStatus
	verification habio.VerificationStatus
	conflicted   bool
}

// NewAttempt creates an empty, fully unknown view.
func NewAttempt(actionID habio.ActionID, attemptID habio.AttemptID) (*Attempt, error) {
	if actionID == "" || attemptID == "" {
		return nil, ErrInvalidIdentity
	}
	return &Attempt{actionID: actionID, attemptID: attemptID}, nil
}

// Apply incorporates one fact in the caller's canonical replay order. Late
// events therefore remain visible in the append log. Weaker dispatch knowledge
// never replaces stronger knowledge, while incompatible claims mark the view
// conflicted and return the affected dimension to unknown.
func (p *Attempt) Apply(event habio.ExecutionEvent) error {
	if event.ActionID() != p.actionID {
		return fmt.Errorf("%w: action %s", ErrUnrelatedEvent, event.ActionID())
	}
	if event.AttemptID() != "" && event.AttemptID() != p.attemptID {
		return fmt.Errorf("%w: attempt %s", ErrUnrelatedEvent, event.AttemptID())
	}

	switch event.Kind() {
	case habio.EventActionRequested, habio.EventAttemptStarted, habio.EventObservationRecorded:
	case habio.EventActionAdmitted:
		p.mergeAdmission(habio.AdmissionAdmitted)
	case habio.EventActionRejected:
		p.mergeAdmission(habio.AdmissionRejected)
		p.mergeDispatch(habio.DispatchNotDispatched)
	case habio.EventDispatchUnknown:
		// Unknown facts do not erase stronger evidence already recorded.
	case habio.EventNotDispatched:
		p.mergeDispatch(habio.DispatchNotDispatched)
	case habio.EventActionDispatched:
		p.mergeDispatch(habio.DispatchDispatched)
	case habio.EventProviderAcknowledged:
		p.mergeDispatch(habio.DispatchAcknowledged)
	case habio.EventEffectUnknown:
	case habio.EventEffectUnverified:
		p.mergeEffect(habio.EffectUnverified)
	case habio.EventEffectObservedSatisfied:
		p.mergeEffect(habio.EffectObservedSatisfied)
	case habio.EventEffectObservedUnsatisfied:
		p.mergeEffect(habio.EffectObservedUnsatisfied)
	case habio.EventVerificationVerified:
		p.verification = habio.VerificationVerified
	case habio.EventVerificationUnsatisfied:
		p.verification = habio.VerificationUnsatisfied
	case habio.EventVerificationInconclusive:
		if p.verification == habio.VerificationUnknown {
			p.verification = habio.VerificationInconclusive
		}
	}
	return nil
}

// Outcome returns the current orthogonal knowledge projection.
func (p *Attempt) Outcome() habio.Outcome {
	outcome, _ := habio.NewOutcome(habio.OutcomeSpec{
		Admission: p.admission, Dispatch: p.dispatch, Effect: p.effect,
	})
	return outcome
}

func (p *Attempt) Verification() habio.VerificationStatus { return p.verification }

func (p *Attempt) Conflicted() bool { return p.conflicted }

func (p *Attempt) mergeAdmission(next habio.AdmissionStatus) {
	if p.admission == habio.AdmissionUnknown || p.admission == next {
		p.admission = next
		return
	}
	p.admission = habio.AdmissionUnknown
	p.conflicted = true
}

func (p *Attempt) mergeDispatch(next habio.DispatchStatus) {
	if p.dispatch == habio.DispatchUnknown || p.dispatch == next {
		p.dispatch = next
		return
	}
	if p.dispatch == habio.DispatchDispatched && next == habio.DispatchAcknowledged {
		p.dispatch = next
		return
	}
	if p.dispatch == habio.DispatchAcknowledged && next == habio.DispatchDispatched {
		return
	}
	p.dispatch = habio.DispatchUnknown
	p.conflicted = true
}

func (p *Attempt) mergeEffect(next habio.EffectStatus) {
	if p.effect == habio.EffectUnknown || p.effect == next || p.effect == habio.EffectUnverified {
		p.effect = next
		return
	}
	if next == habio.EffectUnverified {
		return
	}
	p.effect = habio.EffectUnknown
	p.conflicted = true
}
