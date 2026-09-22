// This executable fixture uses the production CLI entry point and its existing
// HTTP-client dependency to exercise publication without a persisted config.
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"gl-axi/internal/cli"
	"gl-axi/internal/commandctx"
	"gl-axi/internal/product"
	runtimepkg "gl-axi/internal/runtime"
)

func main() {
	client, err := fixtureClient()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	program := strings.TrimSuffix(filepath.Base(os.Args[0]), ".exe")
	os.Exit(commandctx.Run(func(ctx context.Context) int {
		deps := runtimepkg.Defaults()
		deps.HTTPClient = client
		return cli.RunAs(ctx, os.Args[1:], product.DefaultsFor(deps, program), program)
	}))
}

func fixtureClient() (*http.Client, error) {
	address := os.Getenv("GL_AXI_TEST_TLS_ADDRESS")
	host, _, err := net.SplitHostPort(address)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		return nil, errors.New("fixture requires a loopback TLS address")
	}
	ca, err := os.ReadFile(os.Getenv("GL_AXI_TEST_CA_BUNDLE"))
	if err != nil {
		return nil, err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		return nil, errors.New("fixture requires a TLS certificate")
	}
	dialer := &net.Dialer{}
	return &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, network, target string) (net.Conn, error) {
			if target != "gitlab.com:443" {
				return nil, errors.New("unexpected fixture authority")
			}
			return dialer.DialContext(ctx, network, address)
		},
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12, RootCAs: roots, ServerName: host,
		},
		DisableKeepAlives:  true,
		DisableCompression: true,
	}}, nil
}
