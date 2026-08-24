// Command release-manifest derives the release public key or signs a bounded
// update manifest. Private key material is accepted only on stdin.
package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"gl-axi/internal/updater"
)

func main() {
	if len(os.Args) < 2 {
		fail("usage: release-manifest public-key|sign|sign-file")
	}
	private, err := readPrivateKey(os.Stdin)
	if err != nil {
		fail("invalid signing key")
	}
	defer zero(private)
	switch os.Args[1] {
	case "public-key":
		if len(os.Args) != 2 {
			fail("public-key accepts no flags")
		}
		public := private.Public().(ed25519.PublicKey)
		fmt.Println(base64.StdEncoding.EncodeToString(public))
	case "sign":
		flags := flag.NewFlagSet("sign", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		input := flags.String("input", "", "unsigned manifest JSON")
		if err := flags.Parse(os.Args[2:]); err != nil || flags.NArg() != 0 || *input == "" {
			fail("sign requires --input FILE")
		}
		data, err := os.ReadFile(*input)
		if err != nil || len(data) > 1<<20 {
			fail("cannot read unsigned manifest")
		}
		var manifest updater.Manifest
		decoder := json.NewDecoder(strings.NewReader(string(data)))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&manifest); err != nil || manifest.Signature != "" {
			fail("unsigned manifest is malformed")
		}
		var extra any
		if err := decoder.Decode(&extra); err != io.EOF {
			fail("unsigned manifest contains trailing data")
		}
		payload, err := updater.CanonicalPayload(manifest)
		if err != nil {
			fail("cannot canonicalize manifest")
		}
		manifest.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(private, payload))
		encoded, err := json.MarshalIndent(manifest, "", "  ")
		if err != nil {
			fail("cannot encode signed manifest")
		}
		encoded = append(encoded, '\n')
		if _, err := os.Stdout.Write(encoded); err != nil {
			fail("cannot write signed manifest")
		}
	case "sign-file":
		flags := flag.NewFlagSet("sign-file", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		input := flags.String("input", "", "file to sign")
		if err := flags.Parse(os.Args[2:]); err != nil || flags.NArg() != 0 || *input == "" {
			fail("sign-file requires --input FILE")
		}
		data, err := os.ReadFile(*input)
		if err != nil || len(data) > 1<<20 {
			fail("cannot read bounded signing input")
		}
		signature := ed25519.Sign(private, data)
		fmt.Println(base64.StdEncoding.EncodeToString(signature))
	default:
		fail("unknown release-manifest command")
	}
}

func readPrivateKey(reader io.Reader) (ed25519.PrivateKey, error) {
	data, err := io.ReadAll(io.LimitReader(reader, 256))
	if err != nil {
		return nil, err
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(data)))
	zero(data)
	if err != nil {
		return nil, err
	}
	defer zero(decoded)
	switch len(decoded) {
	case ed25519.SeedSize:
		return ed25519.NewKeyFromSeed(decoded), nil
	case ed25519.PrivateKeySize:
		return append(ed25519.PrivateKey(nil), decoded...), nil
	default:
		return nil, fmt.Errorf("unexpected key size")
	}
}

func zero(value []byte) {
	for index := range value {
		value[index] = 0
	}
}

func fail(message string) {
	_, _ = fmt.Fprintln(os.Stderr, message)
	os.Exit(2)
}
