// Package securestore probes the same cross-platform keyring implementation
// pinned by official glab. It writes and deletes only a random non-secret
// sentinel and never reads an official-glab credential or config file.
package securestore

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"

	keyring "github.com/zalando/go-keyring"
)

func Probe(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	service := "gl-axi:__official_glab_keyring_probe__"
	account := hex.EncodeToString(nonce[:])
	if err := keyring.Set(service, account, "1"); err != nil {
		return fmt.Errorf("secure keyring probe write: %w", err)
	}
	if err := keyring.Delete(service, account); err != nil {
		return fmt.Errorf("secure keyring probe cleanup: %w", err)
	}
	return nil
}
