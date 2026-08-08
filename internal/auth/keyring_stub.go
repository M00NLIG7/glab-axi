//go:build !darwin || !cgo

package auth

import "context"

type systemKeyring struct{}

func (systemKeyring) Get(context.Context, string, string) (string, error) {
	return "", ErrKeyringUnavailable
}

func (systemKeyring) Set(context.Context, string, string, string) error {
	return ErrKeyringUnavailable
}

func (systemKeyring) Delete(context.Context, string, string) error {
	return ErrKeyringUnavailable
}
