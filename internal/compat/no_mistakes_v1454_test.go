package compat_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"gl-axi/internal/testgitlab"
)

type contractFile struct {
	Schema string `json:"schema"`
	Source struct {
		Module  string `json:"module"`
		Version string `json:"version"`
		Commit  string `json:"commit"`
	} `json:"source"`
	Vectors []struct {
		Name            string   `json:"name"`
		Argv            []string `json:"argv"`
		Stdout          string   `json:"stdout"`
		Exit            int      `json:"exit"`
		NetworkMutation bool     `json:"network_mutation"`
	} `json:"vectors"`
	Denied [][]string `json:"denied"`
}

type compatAPI struct {
	mu          sync.Mutex
	base        string
	project     string
	projectID   int64
	sha         string
	mr          map[string]any
	token       string
	traceSecret string
}

func TestNoMistakesV1454Contract(t *testing.T) {
	root := repositoryRoot(t)
	contract := loadContract(t, filepath.Join(root, "contracts", "no-mistakes", "v1.45.4.json"))
	if contract.Schema != "glab-axi/no-mistakes-contract-v1" || contract.Source.Version != "v1.45.4" || contract.Source.Commit != "0c58eb7110427cfc1d716641ae4b878e7f8582ac" {
		t.Fatal("contract source identity is not pinned")
	}

	temp := t.TempDir()
	binary := filepath.Join(temp, "glab")
	build := exec.Command("go", "build", "-trimpath", "-o", binary, "./cmd/glab-compat")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build compatibility executable: %v\n%s", err, output)
	}

	repo := filepath.Join(temp, "repo")
	if err := os.Mkdir(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "init", "-q")
	runGit(t, repo, "config", "user.email", "fixture@example.invalid")
	runGit(t, repo, "config", "user.name", "Contract Fixture")
	if err := os.WriteFile(filepath.Join(repo, "fixture.txt"), []byte("fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "fixture.txt")
	runGit(t, repo, "commit", "-q", "-m", "fixture")
	sha := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))

	secret := runtimeToken()
	api := &compatAPI{project: "group/project", projectID: 71, sha: sha, token: secret, traceSecret: secret}
	server := testgitlab.New(http.HandlerFunc(api.serveHTTP))
	defer server.Close()
	api.base = server.HTTP.URL
	ca, err := server.CAFile(temp)
	if err != nil {
		t.Fatal(err)
	}
	remote := server.HTTP.URL + "/group/project.git"
	runGit(t, repo, "remote", "add", "origin", remote)

	host := strings.TrimPrefix(server.HTTP.URL, "https://")
	configPath := filepath.Join(temp, "config.json")
	writeConfig(t, configPath, host, server.HTTP.URL+"/api/v4", server.HTTP.URL, ca)
	env := cleanChildEnvironment(configPath, secret)

	replacements := map[string]string{
		"${host}":            host,
		"${source}":          "feature/contract",
		"${target}":          "main",
		"${title}":           "Contract title",
		"${description}":     "Contract description",
		"${iid}":             "1",
		"${pipeline_id}":     "99",
		"${job_id}":          "100",
		"${encoded_project}": url.PathEscape(api.project),
	}
	for _, vector := range contract.Vectors {
		vector := vector
		t.Run(vector.Name, func(t *testing.T) {
			args := substitute(vector.Argv, replacements)
			if vector.Name == "mr-update" {
				for i := range args {
					if args[i] == "Contract title" {
						args[i] = "Updated contract title"
					}
					if args[i] == "Contract description" {
						args[i] = "Updated contract description"
					}
				}
			}
			before := len(server.Requests())
			stdout, stderr, exit := runCompat(t, binary, repo, env, args)
			requests := server.Requests()[before:]
			mutated := false
			for _, request := range requests {
				if request.Method != http.MethodGet && request.Method != http.MethodHead {
					mutated = true
				}
			}
			if mutated != vector.NetworkMutation {
				t.Fatalf("network mutation=%v, fixture says %v", mutated, vector.NetworkMutation)
			}
			if exit != vector.Exit {
				t.Fatalf("exit=%d, want %d; stderr=%s", exit, vector.Exit, stderr)
			}
			if bytes.Contains(stdout, []byte(secret)) || bytes.Contains(stderr, []byte(secret)) {
				t.Fatal("credential sentinel reached compatibility output")
			}
			if len(stderr) != 0 {
				t.Fatalf("unexpected stderr: %s", stderr)
			}
			assertOutputShape(t, vector.Stdout, stdout)
		})
	}

	for _, denied := range contract.Denied {
		before := len(server.Requests())
		stdout, stderr, exit := runCompat(t, binary, repo, env, denied)
		if exit != 2 || len(stdout) != 0 || len(stderr) == 0 {
			t.Fatalf("forbidden argv did not fail deterministically: %#v", denied)
		}
		if after := len(server.Requests()); after != before {
			t.Fatalf("forbidden argv made %d network request(s): %#v", after-before, denied)
		}
	}

	before := len(server.Requests())
	stdout, stderr, exit := runCompat(t, binary, repo, env, []string{"--help"})
	if exit != 0 || len(stderr) != 0 || !bytes.Contains(stdout, []byte("NOT the\nupstream GitLab CLI")) || len(server.Requests()) != before {
		t.Fatal("compatibility help is not safely branded or made a request")
	}

	for _, request := range server.Requests() {
		if request.Header.Get("PRIVATE-TOKEN") != secret {
			t.Fatal("typed request omitted the expected credential header")
		}
		if strings.Contains(request.URL, secret) || bytes.Contains(request.Body, []byte(secret)) {
			t.Fatal("credential sentinel reached a request URL or body")
		}
	}
	if treeContains(temp, []byte(secret)) {
		t.Fatal("credential sentinel reached an observable file")
	}
}

func (a *compatAPI) serveHTTP(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	projectPath := "/api/v4/projects/" + a.project
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/v4/user":
		writeJSON(w, http.StatusOK, map[string]any{"id": 9, "username": "contract-bot"})
	case r.Method == http.MethodGet && r.URL.Path == projectPath:
		writeJSON(w, http.StatusOK, map[string]any{"id": a.projectID, "path_with_namespace": a.project, "web_url": a.base + "/" + a.project})
	case r.URL.Path == projectPath+"/merge_requests" && r.Method == http.MethodGet:
		if a.mr == nil {
			writeJSON(w, http.StatusOK, []any{})
		} else {
			writeJSON(w, http.StatusOK, []any{a.mr})
		}
	case r.URL.Path == projectPath+"/merge_requests" && r.Method == http.MethodPost:
		var input map[string]any
		_ = json.NewDecoder(r.Body).Decode(&input)
		a.mr = a.makeMR(input["source_branch"].(string), input["target_branch"].(string), input["title"].(string), input["description"].(string))
		writeJSON(w, http.StatusCreated, a.mr)
	case r.URL.Path == projectPath+"/merge_requests/1" && r.Method == http.MethodGet:
		if a.mr == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"message": "not found"})
		} else {
			writeJSON(w, http.StatusOK, a.mr)
		}
	case r.URL.Path == projectPath+"/merge_requests/1" && r.Method == http.MethodPut:
		var input map[string]any
		_ = json.NewDecoder(r.Body).Decode(&input)
		a.mr["title"] = input["title"]
		a.mr["description"] = input["description"]
		writeJSON(w, http.StatusOK, a.mr)
	case r.URL.Path == projectPath+"/pipelines/99/jobs" && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, []any{
			map[string]any{"id": 100, "name": "test", "stage": "test", "status": "success", "allow_failure": false, "finished_at": "2026-08-07T12:00:00Z", "pipeline": map[string]any{"id": 99, "sha": a.sha, "status": "success"}},
			map[string]any{"id": 101, "name": "reflected-" + a.token, "stage": "test", "status": "manual", "allow_failure": true},
		})
	case r.URL.Path == projectPath+"/jobs/100/trace" && r.Method == http.MethodGet:
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "failed command\nPRIVATE-TOKEN: "+a.traceSecret+"\n")
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "not found"})
	}
}

func (a *compatAPI) makeMR(source, target, title, description string) map[string]any {
	return map[string]any{
		"iid": 1, "web_url": a.base + "/" + a.project + "/-/merge_requests/1", "state": "opened",
		"title": title, "description": description, "source_branch": source, "target_branch": target,
		"source_project_id": a.projectID, "target_project_id": a.projectID, "sha": a.sha,
		"has_conflicts": false, "detailed_merge_status": "mergeable", "merge_status": "can_be_merged",
		"head_pipeline": map[string]any{"id": 99, "sha": a.sha, "status": "success"},
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate test source")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func loadContract(t *testing.T, path string) contractFile {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var contract contractFile
	if err := json.Unmarshal(data, &contract); err != nil {
		t.Fatal(err)
	}
	return contract
}

func runtimeToken() string {
	return strings.Join([]string{"glpat", "runtime", "contract", "sentinel"}, "-")
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = dir
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("local Git fixture command failed: %v\n%s", err, output)
	}
	return string(output)
}

func writeConfig(t *testing.T, path, host, apiBase, webBase, ca string) {
	t.Helper()
	value := map[string]any{
		"schema": "glab-axi/config-v1",
		"hosts": map[string]any{
			host: map[string]any{"git_hosts": []string{host}, "api_base": apiBase, "web_base": webBase, "ca_bundle": ca, "proxy_disabled": true},
		},
	}
	data, _ := json.Marshal(value)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func cleanChildEnvironment(configPath, token string) []string {
	var env []string
	for _, item := range os.Environ() {
		name := strings.SplitN(item, "=", 2)[0]
		switch name {
		case "GLAB_AXI_TOKEN", "GITLAB_TOKEN", "GITLAB_ACCESS_TOKEN", "OAUTH_TOKEN", "GLAB_AXI_CONFIG", "GLAB_AXI_CONFIG_DIR":
			continue
		}
		env = append(env, item)
	}
	return append(env, "GLAB_AXI_CONFIG="+configPath, "GLAB_AXI_TOKEN="+token)
}

func substitute(args []string, replacements map[string]string) []string {
	out := make([]string, len(args))
	for i, arg := range args {
		out[i] = arg
		for placeholder, value := range replacements {
			out[i] = strings.ReplaceAll(out[i], placeholder, value)
		}
	}
	return out
}

func runCompat(t *testing.T, binary, cwd string, env, args []string) ([]byte, []byte, int) {
	t.Helper()
	command := exec.Command(binary, args...)
	command.Dir = cwd
	command.Env = env
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	if err == nil {
		return stdout.Bytes(), stderr.Bytes(), 0
	}
	if exit, ok := err.(*exec.ExitError); ok {
		return stdout.Bytes(), stderr.Bytes(), exit.ExitCode()
	}
	t.Fatalf("execute compatibility binary: %v", err)
	return nil, nil, -1
}

func assertOutputShape(t *testing.T, shape string, stdout []byte) {
	t.Helper()
	switch shape {
	case "empty":
		if len(stdout) != 0 {
			t.Fatal("expected empty stdout")
		}
	case "json-array":
		var value []any
		if json.Unmarshal(stdout, &value) != nil {
			t.Fatal("expected one JSON array")
		}
	case "json-object":
		var value map[string]any
		if json.Unmarshal(stdout, &value) != nil {
			t.Fatal("expected one JSON object")
		}
	case "raw-trace":
		if !bytes.Contains(stdout, []byte("failed command")) || !bytes.Contains(stdout, []byte("[REDACTED]")) {
			t.Fatal("trace was not returned and redacted")
		}
	default:
		t.Fatalf("unknown output shape %q", shape)
	}
}

func treeContains(root string, needle []byte) bool {
	found := false
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || found || entry.IsDir() {
			return nil
		}
		file, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer file.Close()
		data, _ := io.ReadAll(file)
		if bytes.Contains(data, needle) {
			found = true
		}
		return nil
	})
	return found
}
