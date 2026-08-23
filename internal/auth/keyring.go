package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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
	Delete(context.Context, string, string) error
}

// Probe verifies that a keyring can complete a noninteractive write/delete
// cycle without reading or modifying a credential. The random account avoids
// collisions with both gl-axi credentials and official-glab entries.
func Probe(ctx context.Context, keyring Keyring) error {
	if keyring == nil {
		return ErrKeyringUnavailable
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	account := hex.EncodeToString(nonce[:])
	const service = "gl-axi/secure-store-probe"
	if err := keyring.Set(ctx, service, account, "1"); err != nil {
		return err
	}
	if err := keyring.Delete(ctx, service, account); err != nil {
		return err
	}
	return nil
}

func SystemKeyring() Keyring { return systemKeyring{} }
