package kopper

import (
	"errors"
	"fmt"
)

// ReasonInvalid is the default reason for resources rejected by OnUpsertFunc.
const ReasonInvalid = "Invalid"

// ConditionError is returned by an OnUpsertFunc to report that a resource
// cannot take effect in its current state, e.g. because it fails validation
// or depends on something that doesn't exist yet.
//
// Unlike other errors it isn't retried: Kopper sets the Ready condition to
// False with the given Reason and the error as the message, records a
// Warning event, and waits for the resource to change or for a Resync/Enqueue.
type ConditionError struct {
	Reason string
	Err    error
}

func (e *ConditionError) Error() string {
	if e.Err == nil {
		return e.Reason
	}
	return e.Err.Error()
}

func (e *ConditionError) Unwrap() error {
	return e.Err
}

// NewConditionError wraps err so the Ready condition is set to False with the given reason.
// An empty reason defaults to ReasonInvalid.
func NewConditionError(reason string, err error) error {
	if reason == "" {
		reason = ReasonInvalid
	}
	return &ConditionError{Reason: reason, Err: err}
}

// ConditionErrorf is NewConditionError with a formatted message.
func ConditionErrorf(reason, format string, args ...any) error {
	return NewConditionError(reason, fmt.Errorf(format, args...))
}

// AsConditionError returns the ConditionError in err's chain, if any.
func AsConditionError(err error) (*ConditionError, bool) {
	var ce *ConditionError
	if errors.As(err, &ce) {
		return ce, true
	}
	return nil, false
}
