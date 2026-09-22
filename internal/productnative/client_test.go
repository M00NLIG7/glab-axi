package productnative

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gl-axi/internal/auth"
	"gl-axi/internal/config"
	"gl-axi/internal/contract/uxv1"
)

func fixture(t *testing.T, handler http.HandlerFunc) (*Client, *httptest.Server, string) {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	cfg := config.New()
	if err := cfg.Put("native.example", config.Host{GitHosts: []string{"native.example"}, APIBase: server.URL + "/api/v4", WebBase: server.URL, ProxyDisabled: true}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "private", "config.json")
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	token := strings.Join([]string{"synthetic", "native", "identity"}, "-")
	client, err := Open(context.Background(), Options{Host: "native.example", ConfigPath: path, HTTPClient: server.Client(), LookupEnv: func(k string) (string, bool) { return token, k == "GL_AXI_TOKEN" }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	return client, server, token
}

func TestNativeRefusesAllRedirectsBeforeSecondRequest(t *testing.T) {
	for _, method := range []string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE"} {
		for _, status := range []int{301, 302, 303, 307, 308} {
			for _, cross := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%d/cross=%t", method, status, cross), func(t *testing.T) {
					var origin, sibling, target atomic.Int32
					other := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { target.Add(1); w.WriteHeader(200) }))
					defer other.Close()
					client, _, _ := fixture(t, func(w http.ResponseWriter, r *http.Request) {
						if r.URL.Path != "/api/v4/projects/101/jobs/42/artifacts" {
							sibling.Add(1)
							w.WriteHeader(200)
							return
						}
						origin.Add(1)
						location := "/api/v4/projects/202/jobs/99/artifacts"
						if cross {
							location = other.URL + location
						}
						w.Header().Set("Location", location)
						w.WriteHeader(status)
					})
					response, err := client.Do(context.Background(), Request{Method: method, Path: "projects/101/jobs/42/artifacts", MaxBytes: 1024})
					if err == nil || uxv1.AsError(err).Code != uxv1.CodeSafety || response.StatusCode != status || origin.Load() != 1 || sibling.Load() != 0 || target.Load() != 0 {
						t.Fatalf("response=%+v err=%v requests=%d/%d/%d", response, err, origin.Load(), sibling.Load(), target.Load())
					}
				})
			}
		}
	}
}

func TestNativeOneIdentityBoundsAndNoRetry(t *testing.T) {
	var requests atomic.Int32
	var state atomic.Int32
	client, _, token := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("Private-Token") != strings.Join([]string{"synthetic", "native", "identity"}, "-") {
			t.Error("wrong synthetic identity")
		}
		switch state.Load() {
		case 1:
			w.WriteHeader(503)
		case 2:
			_, _ = w.Write(bytes.Repeat([]byte("a"), 2049))
		case 3:
			w.Header().Set("Content-Length", "10")
			_, _ = w.Write([]byte("short"))
		case 4:
			_, _ = w.Write([]byte(strings.Join([]string{"synthetic", "native", "identity"}, "-")))
		default:
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Set-Cookie", "not-public")
			_, _ = w.Write([]byte(`{"id":101}`))
		}
	})
	r := Request{Method: "GET", Path: "projects/group%2Fproject", MaxBytes: 2048}
	for mode := int32(0); mode < 5; mode++ {
		state.Store(mode)
		before := requests.Load()
		response, err := client.Do(context.Background(), r)
		if requests.Load() != before+1 {
			t.Fatal("request replayed")
		}
		if mode == 0 {
			if err != nil || string(response.Body) != `{"id":101}` || response.Header.Get("Set-Cookie") != "" {
				t.Fatalf("response=%+v err=%v", response, err)
			}
		} else if err == nil || len(response.Body) != 0 || strings.Contains(err.Error(), token) {
			t.Fatalf("mode=%d response=%+v err=%v", mode, response, err)
		}
	}
	host := client.Host()
	host.Authority.API.Host = "other.example"
	if client.Host().Authority.API.Host == "other.example" {
		t.Fatal("authority was mutable")
	}
}

func TestNativeValidationBeforeTransmission(t *testing.T) {
	var requests atomic.Int32
	client, _, _ := fixture(t, func(w http.ResponseWriter, r *http.Request) { requests.Add(1) })
	for _, path := range []string{"https://evil.example/x", "//evil.example/x", "projects/../users", "projects/%2e%2e/users", "projects/%252e%252e/users", "projects//users", "projects/x?token=x", "projects/x#fragment", "projects/x\\y", "projects/%00"} {
		if _, err := client.Do(context.Background(), Request{Method: "GET", Path: path, MaxBytes: 1024}); err == nil {
			t.Fatalf("accepted path %q", path)
		}
	}
	for _, header := range []string{"Host", "Private-Token", "Authorization", "Cookie", "Proxy-Authorization", "X-Forwarded-Host", "Content-Length", "Transfer-Encoding", "Idempotency-Key"} {
		if _, err := client.Do(context.Background(), Request{Method: "GET", Path: "projects/101", MaxBytes: 1024, Headers: http.Header{header: {"value"}}}); err == nil {
			t.Fatalf("accepted header %s", header)
		}
	}
	if _, err := client.Do(context.Background(), Request{Method: "GET", Path: "projects/101", MaxBytes: 1024, Query: url.Values{"job_token": {"value"}}}); err == nil {
		t.Fatal("accepted credential query")
	}
	if requests.Load() != 0 {
		t.Fatal("invalid request transmitted")
	}
}

func TestNativeStreamCancelCredentialEchoAndBudgets(t *testing.T) {
	var mode atomic.Int32
	client, _, _ := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch mode.Load() {
		case 1:
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		case 2:
			_, _ = w.Write([]byte(strings.Join([]string{"synthetic", "native", "identity"}, "-")))
		default:
			_, _ = w.Write([]byte("archive"))
		}
	})
	var dst bytes.Buffer
	r := Request{Method: "GET", Path: "projects/101/jobs/42/artifacts", MaxBytes: 1024}
	response, err := client.Stream(context.Background(), r, &dst)
	if err != nil || response.Bytes != 7 || dst.String() != "archive" {
		t.Fatalf("response=%+v err=%v", response, err)
	}
	mode.Store(2)
	dst.Reset()
	if _, err := client.Stream(context.Background(), r, &dst); err == nil || dst.Len() != 0 {
		t.Fatal("credential echo escaped")
	}
	mode.Store(1)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := client.Stream(ctx, r, &dst); err == nil {
		t.Fatal("interrupted stream succeeded")
	}
	mode.Store(0)
	client.requests = MaxRequests
	if _, err := client.Do(context.Background(), r); err == nil {
		t.Fatal("request budget ignored")
	}
	client.requests = 0
	client.buffered = MaxBytes
	if _, err := client.Do(context.Background(), r); err == nil {
		t.Fatal("byte budget ignored")
	}
	client.Close()
	if _, err := client.Do(context.Background(), r); uxv1.AsError(err).Code != uxv1.CodeCanceled {
		t.Fatalf("closed: %v", err)
	}
}

func TestCredentialScannerAcrossChunks(t *testing.T) {
	var dst bytes.Buffer
	w := credentialWriter{dst: &dst, secrets: [][]byte{[]byte("runtime-sentinel")}}
	if _, err := w.Write([]byte("prefix-runtime-")); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("sentinel-suffix")); err == nil {
		t.Fatal("split credential escaped")
	}
	if strings.Contains(dst.String(), "runtime-sentinel") {
		t.Fatal("credential written")
	}
}

type absentKeyring struct{ calls int }

func (k *absentKeyring) Get(context.Context, string, string) (string, error) {
	k.calls++
	return "", auth.ErrKeyringNotFound
}
func (*absentKeyring) Set(context.Context, string, string, string) error {
	return fmt.Errorf("unexpected set")
}
func (*absentKeyring) Delete(context.Context, string, string) error {
	return fmt.Errorf("unexpected delete")
}

func TestNativeMissingCredentialAndWrongHostNeverFallback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent-config.json")
	keyring := &absentKeyring{}
	opts := Options{Host: "gitlab.com", ConfigPath: path, LookupEnv: func(string) (string, bool) { return "", false }, Keyring: keyring}
	if _, err := Open(context.Background(), opts); uxv1.AsError(err).Code != uxv1.CodeAuthentication || keyring.calls != 1 {
		t.Fatalf("missing credential err=%v calls=%d", err, keyring.calls)
	}
	opts.Host = "unconfigured.example"
	if _, err := Open(context.Background(), opts); err == nil || keyring.calls != 1 {
		t.Fatalf("wrong host err=%v calls=%d", err, keyring.calls)
	}
}
