package product

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"

	"gl-axi/internal/contract/uxv1"
	"gl-axi/internal/delegate/glab"
)

const (
	issueWriteProjectOperation glab.Operation = "issue-write-project"
	issueWriteViewOperation    glab.Operation = "issue-write-view"
	issueCreateOperation       glab.Operation = "issue-create"
	issueNoteCreateOperation   glab.Operation = "issue-note-create"
	issueStateOperation        glab.Operation = "issue-state"
)

// Retain the feature's exhaustive response/drift fixtures while exercising the
// landed productnative client, not a production delegate or replacement auth
// resolver. TLS and compiled-executable tests separately exercise real sockets.
type issueWriteFixtureTransport struct {
	t         *testing.T
	responses *fakeDelegate
}

func (f issueWriteFixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	f.t.Helper()
	request := glab.Request{Host: r.URL.Host, Repo: "group/project", IID: 42}
	switch r.Method + " " + r.URL.EscapedPath() {
	case "GET /api/v4/projects/group%2Fproject":
		request.Operation = issueWriteProjectOperation
	case "GET /api/v4/projects/101/issues/42":
		request.Operation = issueWriteViewOperation
	case "POST /api/v4/projects/101/issues":
		request.Operation = issueCreateOperation
	case "POST /api/v4/projects/101/issues/42/notes":
		request.Operation = issueNoteCreateOperation
	case "PUT /api/v4/projects/101/issues/42":
		request.Operation = issueStateOperation
	default:
		f.t.Fatal("unexpected native issue fixture route")
	}
	if r.Method != "GET" {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		f.responses.inputBodies = append(f.responses.inputBodies, body)
		if r.Header.Get("Content-Type") != "application/json" {
			f.t.Fatal("missing native JSON content type")
		}
	}
	response, err := f.responses.Do(r.Context(), request)
	status := http.StatusOK
	if err != nil {
		if rejection := uxv1.AsError(err); rejection.StatusCode > 0 {
			status = rejection.StatusCode
		} else {
			return nil, err
		}
	}
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(bytes.NewReader(response.Body)), ContentLength: int64(len(response.Body)), Request: r}, nil
}

func issueWriteTestDeps(t *testing.T, d *fakeDelegate) (*bytes.Buffer, *bytes.Buffer, Dependencies) {
	t.Helper()
	out, stderr, deps := productTestDeps(t, d)
	deps.Runtime.ConfigPath = deps.Runtime.Cwd + "/absent-config.json"
	token := strings.Join([]string{"synthetic", "issue", "unit", "credential"}, "-")
	deps.Runtime.LookupEnv = func(name string) (string, bool) { return token, name == "GL_AXI_TOKEN" }
	deps.Runtime.Keyring = &issueNativeKeyring{}
	deps.Runtime.HTTPClient = &http.Client{Transport: issueWriteFixtureTransport{t, d}}
	deps.NewDelegate = func() delegateClient { t.Error("native issue fixture constructed a delegated child"); return d }
	return out, stderr, deps
}

// Compile-time assertion keeps this fixture on the existing HTTP injection
// seam. It never exports credentials or creates a new product transport.
var _ http.RoundTripper = issueWriteFixtureTransport{}
