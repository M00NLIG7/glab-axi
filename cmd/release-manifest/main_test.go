package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"testing"
)

func TestReadPrivateKeyAcceptsOnlyBoundedBase64SeedOrKey(t *testing.T) {
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range [][]byte{private.Seed(), private} {
		encoded := base64.StdEncoding.EncodeToString(value)
		got, err := readPrivateKey(bytes.NewBufferString(encoded + "\n"))
		if err != nil || !bytes.Equal(got, private) {
			t.Fatalf("key size=%d err=%v", len(value), err)
		}
		zero(got)
	}
	if _, err := readPrivateKey(bytes.NewBufferString(base64.StdEncoding.EncodeToString([]byte("too short")))); err == nil {
		t.Fatal("short key was accepted")
	}
}
