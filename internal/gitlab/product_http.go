package gitlab

import (
	"crypto/tls"
	"net/http"

	"gl-axi/internal/config"
)

// NewProductHTTPClient reuses native TLS/authority configuration for explicitly
// opted-in product operations. It does not change the frozen native client's
// redirect semantics. The injected client is for isolated protocol tests.
func NewProductHTTPClient(host config.ResolvedHost, injected *http.Client) (*http.Client, error) {
	var client http.Client
	if injected != nil {
		client = *injected
	} else {
		transport, err := transportFor(host)
		if err != nil {
			return nil, err
		}
		// HTTP/2 can replay refused streams even on fresh connections. Pin
		// HTTP/1.1 and disable reuse so neither protocol silently retries.
		// TLS verification and authority selection remain unchanged.
		transport.ForceAttemptHTTP2 = false
		transport.TLSNextProto = make(map[string]func(string, *tls.Conn) http.RoundTripper)
		transport.DisableKeepAlives = true
		transport.DisableCompression = true
		client.Transport = transport
	}
	client.Jar = nil
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &client, nil
}
