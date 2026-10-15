package cel2filter

import (
	"errors"
	"fmt"
)

// Sentinel errors. Callers use errors.Is to tell them apart.
var (
	// ErrNilAST reports a nil *cel.Ast.
	ErrNilAST = errors.New("nil ast")

	// ErrUncheckedAST reports an AST that was parsed but not type-checked. Translation reads the checked type
	// of every sub-expression, so only cel.Env.Compile or cel.Env.Check results are accepted.
	ErrUncheckedAST = errors.New("ast is not type-checked")

	// ErrNotBoolean reports an expression whose result type is not bool.
	ErrNotBoolean = errors.New("expression is not boolean")

	// ErrRootNotFound reports that the root variable could not be inferred: the expression references no
	// variable, or more than one. Use WithRoot.
	ErrRootNotFound = errors.New("root variable not found")

	// ErrInvalidOption reports an option with an unusable value.
	ErrInvalidOption = errors.New("invalid option")
)

// ConversionError carries a user-safe message next to the details that belong in a log. Error returns only
// the user message; Internal returns the details.
type ConversionError struct {
	// UserMessage is safe to show to the caller of an API.
	UserMessage string
	// InternalDetails is for logs and debugging and should not reach end users.
	InternalDetails string
	// WrappedErr is the underlying error, if any.
	WrappedErr error
}

// Error returns the user-facing message.
func (e *ConversionError) Error() string { return e.UserMessage }

// Unwrap returns the wrapped error so errors.Is and errors.As see through it.
func (e *ConversionError) Unwrap() error { return e.WrappedErr }

// Internal returns the message with the details appended.
func (e *ConversionError) Internal() string {
	if e.InternalDetails == "" {
		return e.UserMessage
	}
	return e.UserMessage + ": " + e.InternalDetails
}

func wrap(sentinel error, details string) error {
	return &ConversionError{UserMessage: sentinel.Error(), InternalDetails: details, WrappedErr: sentinel}
}

func wrapf(sentinel error, format string, args ...any) error {
	return wrap(sentinel, fmt.Sprintf(format, args...))
}
