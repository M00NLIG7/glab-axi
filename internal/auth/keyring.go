package auth

import (
	"context"
	"errors"
)

var (
	ErrKeyringNotFound    = errors.New("keyring item not found")
	ErrKeyringUnavailable = errors.New("keyring unavailable")
)

// Keyring is intentionally narrow. Implementations must never place secret
// values in process arguments, files, logs, or interactive prompts.
type Keyring interface {
	Get(context.Context, string, string) (string, error)
	Set(context.Context, string, string, string) error
}

func SystemKeyring() Keyring { return systemKeyring{} }
