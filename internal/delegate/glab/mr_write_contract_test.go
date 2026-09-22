package glab

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"gl-axi/internal/contract/uxv1"
)

func TestMRWriteFixedArgv(t *testing.T) {
	input := filepath.Join(t.TempDir(), "body.json")
	for _, tc := range []struct {
		op             Operation
		method, suffix string
		write          bool
	}{
		{OpMRStateUpdate, "PUT", "", true}, {OpMRNoteCreate, "POST", "/notes", true}, {OpMRNoteView, "GET", "/notes/501", false},
	} {
		req := Request{Operation: tc.op, Host: "gitlab.example.invalid", Repo: "group/subgroup/project", IID: 42, ID: 501, InputFile: input}
		got, err := build(req)
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"api", "--method", tc.method, "--hostname", req.Host, "projects/group%2Fsubgroup%2Fproject/merge_requests/42" + tc.suffix}
		if tc.write {
			want = append(want, "--input", input, "--header", "Content-Type: application/json")
		}
		if !reflect.DeepEqual(got.args, want) || got.write != tc.write {
			t.Fatalf("invocation=%#v want=%v", got, want)
		}
		req.IID = 0
		if _, err := build(req); err == nil {
			t.Fatal("accepted invalid IID")
		}
		req.IID = 42
		req.InputFile = "relative"
		req.ID = 0
		if _, err := build(req); err == nil {
			t.Fatal("accepted unsafe input or note ID")
		}
	}
}

// The actual pinned official CLI reaches only a local synthetic TLS server.
// Mutations send one request even for rejection, 5xx and redirect. The pinned
// read client follows at most ten same-host redirects before failing.
func TestPinnedOfficialGlabMRWritesTLS(t *testing.T) {
	binary := officialGlabTestBinary()
	if binary == "" {
		t.Skip("official-glab package fixture not supplied")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	host := "gitlab.mr-write-contract.example"
	certificate, caPEM := selfManagedTestCertificate(t, host)
	home := t.TempDir()
	ca := filepath.Join(home, "ca.pem")
	if err := os.WriteFile(ca, caPEM, 0600); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(home, "config")
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config, "config.yml"), []byte(fmt.Sprintf("hosts:\n  %s:\n    api_protocol: https\n    ca_cert: %s\n", host, ca)), 0600); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var records []capturedOfficialGlabMutation
	status := 200
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, readErr := io.ReadAll(http.MaxBytesReader(w, r.Body, 256<<10))
		mu.Lock()
		defer mu.Unlock()
		records = append(records, capturedOfficialGlabMutation{method: r.Method, host: r.Host, requestURI: r.RequestURI, contentType: r.Header.Get("Content-Type"), body: body, readErr: readErr})
		w.Header().Set("Content-Type", "application/json")
		if status == 307 {
			w.Header().Set("Location", "https://"+host+"/unexpected")
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, `{"synthetic":true}`)
	}))
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate}}
	server.StartTLS()
	defer server.Close()
	proxy := newTestTLSTunnelProxy(host+":443", server.Listener.Addr().String())
	defer proxy.Close()
	token := strings.Join([]string{"synthetic", "mr-write", "token"}, "-")
	env := []string{"HOME=" + home, "GLAB_CONFIG_DIR=" + config, "GITLAB_TOKEN=" + token, "HTTPS_PROXY=" + proxy.URL, "NO_PROXY=", "SSL_CERT_FILE=" + ca, "PATH=/usr/bin:/bin"}
	for _, tc := range []struct {
		op           Operation
		method, path string
		payload      map[string]any
	}{
		{OpMRNoteCreate, "POST", "/merge_requests/42/notes", map[string]any{"body": "A synthetic note."}},
		{OpMRStateUpdate, "PUT", "/merge_requests/42", map[string]any{"state_event": "close"}},
		{OpMRStateUpdate, "PUT", "/merge_requests/42", map[string]any{"state_event": "reopen"}},
		{OpEnsureCreate, "POST", "/merge_requests", map[string]any{"source_branch": "feature", "target_branch": "main", "title": "Draft: title", "description": "body", "assignee_ids": []any{float64(7)}, "reviewer_ids": []any{float64(8)}, "milestone_id": float64(9)}},
		{OpMRNoteView, "GET", "/merge_requests/42/notes/501", nil},
	} {
		for _, desiredStatus := range []int{200, 403, 409, 422, 429, 500, 307} {
			t.Run(fmt.Sprintf("%s-%v-%d", tc.op, tc.payload, desiredStatus), func(t *testing.T) {
				mu.Lock()
				status = desiredStatus
				before := len(records)
				mu.Unlock()
				input := ""
				if tc.payload != nil {
					encoded, err := json.Marshal(tc.payload)
					if err != nil {
						t.Fatal(err)
					}
					input = filepath.Join(home, "input.json")
					if err := os.WriteFile(input, encoded, 0600); err != nil {
						t.Fatal(err)
					}
				}
				client := NewClient(ClientConfig{Path: binary, Env: env})
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				response, requestErr := client.Do(ctx, Request{Operation: tc.op, Host: host, Repo: "group/project", IID: 42, ID: 501, InputFile: input})
				if desiredStatus == 200 && requestErr != nil {
					t.Fatalf("request=%v", requestErr)
				}
				if desiredStatus != 200 && requestErr == nil {
					t.Fatal("rejection/redirect succeeded")
				}
				if tc.payload != nil && desiredStatus >= 400 && desiredStatus < 500 && uxv1.AsError(requestErr).StatusCode != desiredStatus {
					t.Fatalf("status=%d error=%#v", desiredStatus, uxv1.AsError(requestErr))
				}
				if response.Write != (tc.payload != nil) {
					t.Fatal("incorrect mutation receipt")
				}
				mu.Lock()
				defer mu.Unlock()
				wantRequests := 1
				if tc.payload == nil && desiredStatus == 307 {
					wantRequests = 10
				}
				if len(records) != before+wantRequests {
					t.Fatalf("requests=%d want=%d", len(records)-before, wantRequests)
				}
				got := records[before]
				if got.readErr != nil || got.method != tc.method || got.host != host || got.requestURI != "/api/v4/projects/group%2Fproject"+tc.path {
					t.Fatalf("request=%#v", got)
				}
				if tc.payload != nil {
					var payload map[string]any
					if err := json.Unmarshal(got.body, &payload); err != nil || !reflect.DeepEqual(payload, tc.payload) {
						t.Fatalf("body=%s expected=%v error=%v", got.body, tc.payload, err)
					}
				}
				if strings.Contains(got.requestURI, token) || strings.Contains(string(got.body), token) {
					t.Fatal("credential in request target/body")
				}
			})
		}
	}
}
