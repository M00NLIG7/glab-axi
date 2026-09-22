package product

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/url"
	"strconv"
	"strings"

	"gl-axi/internal/contract/uxv1"
	"gl-axi/internal/limits"
	"gl-axi/internal/productnative"
	"gl-axi/internal/safedownload"
	"gl-axi/internal/safeurl"
)

func downloadFlags(job, metadata bool) []FlagDefinition {
	flags := []FlagDefinition{{Name: "--expected-sha", Value: "SHA", Description: "Exact lowercase commit SHA for the selected pipeline or release tag.", Required: true}}
	if job {
		flags = append(flags,
			FlagDefinition{Name: "--pipeline-id", Value: "ID", Description: "Exact owning pipeline ID.", Required: true},
			FlagDefinition{Name: "--expected-ref", Value: "REF", Description: "Exact job and pipeline ref.", Required: true},
		)
	} else {
		flags = append(flags,
			FlagDefinition{Name: "--asset-id", Value: "ID", Description: "Exact release link ID.", Required: true},
			FlagDefinition{Name: "--asset-name", Value: "NAME", Description: "Exact release link name, not a glob.", Required: true},
		)
	}
	if !metadata {
		flags = append(flags, FlagDefinition{Name: "--destination", Value: "DIRECTORY", Description: "Absolute nonexistent output directory; parent must exist without symlinks. Never merges into existing files.", Required: true})
	}
	return flags
}

func validateDownloadParsed(p Parsed) error {
	if !validMergeSHA(p.Values["--expected-sha"]) {
		return uxv1.NewError(uxv1.CodeValidation, "--expected-sha must be a lowercase 40- or 64-hex commit SHA")
	}
	if p.Definition.Path[0] == "job" {
		if _, err := downloadID(p.Positionals[0]); err != nil {
			return err
		}
		if _, err := downloadID(p.Values["--pipeline-id"]); err != nil {
			return err
		}
		if err := safeurl.ValidateBranch(p.Values["--expected-ref"]); err != nil {
			return uxv1.NewError(uxv1.CodeValidation, "--expected-ref must be a valid exact Git ref")
		}
	} else {
		if err := safeurl.ValidateBranch(p.Positionals[0]); err != nil {
			return uxv1.NewError(uxv1.CodeValidation, "release tag must be a valid exact Git tag")
		}
		if _, err := downloadID(p.Values["--asset-id"]); err != nil {
			return err
		}
		if !safedownload.ValidFileName(p.Values["--asset-name"]) {
			return uxv1.NewError(uxv1.CodeValidation, "asset name must be an exact portable filename")
		}
	}
	if p.Definition.Path[1] != "artifacts" {
		if err := safedownload.ValidateDestination(p.Values["--destination"]); err != nil {
			return uxv1.NewError(uxv1.CodeValidation, "destination must be an absolute clean new-directory path")
		}
	}
	return nil
}
func downloadID(s string) (int64, error) {
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != s {
		return 0, uxv1.NewError(uxv1.CodeValidation, "download identities must be canonical positive integers")
	}
	return id, nil
}
func downloadSafety() error {
	return uxv1.NewError(uxv1.CodeSafety, "download provider identity, selection, or integrity did not match the exact target")
}

type downloadProject struct {
	ID   int64  `json:"id"`
	Path string `json:"path_with_namespace"`
	URL  string `json:"web_url"`
}
type downloadPipeline struct {
	ID        int64  `json:"id"`
	ProjectID int64  `json:"project_id"`
	Ref       string `json:"ref"`
	SHA       string `json:"sha"`
	URL       string `json:"web_url"`
}
type downloadJob struct {
	ID       int64            `json:"id"`
	Ref      string           `json:"ref"`
	URL      string           `json:"web_url"`
	Pipeline downloadPipeline `json:"pipeline"`
	Commit   struct {
		ID string `json:"id"`
	} `json:"commit"`
	Archive struct {
		Filename string `json:"filename"`
		Size     int64  `json:"size"`
	} `json:"artifacts_file"`
}
type artifactMetadata struct {
	ProjectID  int64  `json:"project_id"`
	PipelineID int64  `json:"pipeline_id"`
	JobID      int64  `json:"job_id"`
	Ref        string `json:"ref"`
	SHA        string `json:"sha"`
	Filename   string `json:"filename"`
	Size       int64  `json:"size"`
}
type downloadReceipt struct {
	Kind          string `json:"kind"`
	ProjectID     int64  `json:"project_id"`
	Project       string `json:"project"`
	PipelineID    int64  `json:"pipeline_id,omitempty"`
	JobID         int64  `json:"job_id,omitempty"`
	Ref           string `json:"ref,omitempty"`
	SHA           string `json:"sha"`
	Tag           string `json:"tag,omitempty"`
	AssetID       int64  `json:"asset_id,omitempty"`
	AssetName     string `json:"asset_name,omitempty"`
	PackageID     int64  `json:"package_id,omitempty"`
	PackageFileID int64  `json:"package_file_id,omitempty"`
	Destination   string `json:"destination"`
	Bytes         int64  `json:"bytes"`
	SHA256        string `json:"sha256"`
	Checksum      string `json:"checksum"`
	Files         int    `json:"files"`
	ExpandedBytes int64  `json:"expanded_bytes"`
}

func downloadJSON(ctx context.Context, c *productnative.Client, route string, query url.Values, out any) (productnative.Response, error) {
	response, err := c.Do(ctx, productnative.Request{Method: "GET", Path: route, Query: query, MaxBytes: limits.MaxJSONPageBytes})
	if err != nil {
		return response, err
	}
	media, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		return response, uxv1.NewError(uxv1.CodeUpstream, "download metadata must be JSON")
	}
	trimmed := bytes.TrimSpace(response.Body)
	if len(trimmed) == 0 || (trimmed[0] != '{' && trimmed[0] != '[') || validateUniqueJSON(response.Body, trimmed[0], "download metadata") != nil {
		return response, uxv1.NewError(uxv1.CodeUpstream, "download metadata is malformed or contains duplicate fields")
	}
	if err := json.Unmarshal(response.Body, out); err != nil {
		return response, uxv1.NewError(uxv1.CodeUpstream, "download metadata is malformed")
	}
	return response, nil
}
func readDownloadProject(ctx context.Context, c *productnative.Client, repo string) (downloadProject, error) {
	var project downloadProject
	_, err := downloadJSON(ctx, c, "projects/"+url.PathEscape(repo), nil, &project)
	if err != nil {
		return project, err
	}
	if project.ID <= 0 || project.Path != repo || c.Host().Authority.ValidateProjectWebURL(project.URL, repo) != nil {
		return project, downloadSafety()
	}
	return project, nil
}
func readDownloadJob(ctx context.Context, c *productnative.Client, project downloadProject, id, pipelineID int64, ref, sha string) (artifactMetadata, error) {
	base := fmt.Sprintf("projects/%d", project.ID)
	var job downloadJob
	if _, err := downloadJSON(ctx, c, fmt.Sprintf("%s/jobs/%d", base, id), nil, &job); err != nil {
		return artifactMetadata{}, err
	}
	if pipelineID == 0 {
		pipelineID = job.Pipeline.ID
	}
	if ref == "" {
		ref = job.Ref
	}
	var pipeline downloadPipeline
	if _, err := downloadJSON(ctx, c, fmt.Sprintf("%s/pipelines/%d", base, pipelineID), nil, &pipeline); err != nil {
		return artifactMetadata{}, err
	}
	if job.ID != id || job.URL != fmt.Sprintf("%s/-/jobs/%d", project.URL, id) || job.Pipeline.ID != pipelineID || job.Pipeline.ProjectID != project.ID || job.Pipeline.SHA != sha || job.Pipeline.Ref != ref || job.Ref != ref || job.Commit.ID != sha || pipeline.ID != pipelineID || pipeline.ProjectID != project.ID || pipeline.SHA != sha || pipeline.Ref != ref || pipeline.URL != fmt.Sprintf("%s/-/pipelines/%d", project.URL, pipelineID) || !safedownload.ValidFileName(job.Archive.Filename) || job.Archive.Size <= 0 {
		return artifactMetadata{}, downloadSafety()
	}
	return artifactMetadata{project.ID, pipelineID, id, ref, sha, job.Archive.Filename, job.Archive.Size}, nil
}

func executeNativeDownload(ctx context.Context, p Parsed, deps Dependencies, meta uxv1.Meta) (out commandOutput, err error) {
	defer func() {
		if errors.Is(err, context.Canceled) {
			err = uxv1.Wrap(uxv1.CodeCanceled, "native GitLab operation canceled", err)
		} else if errors.Is(err, context.DeadlineExceeded) {
			err = uxv1.Wrap(uxv1.CodeUpstream, "native GitLab operation deadline exceeded", err)
		}
	}()
	meta.Backend = "native"
	meta.UpstreamVersion = ""
	meta.Limit = 0
	out.meta = meta
	var tx *safedownload.Transaction
	if p.Definition.Path[1] != "artifacts" {
		tx, err = safedownload.Prepare(p.Values["--destination"])
		if err != nil {
			return out, uxv1.NewError(uxv1.CodeSafety, "download destination is existing, unsafe, or unavailable")
		}
		defer func() {
			if cleanup := tx.Close(); cleanup != nil {
				err = uxv1.NewError(uxv1.CodeSafety, "download staging cleanup incomplete; caller files preserved")
			}
		}()
	}
	client, err := openNative(ctx, p, deps)
	if err != nil {
		return out, err
	}
	defer client.Close()
	out.meta.Host = client.Host().Name
	project, err := readDownloadProject(ctx, client, p.Values["--repo"])
	if err != nil {
		return out, err
	}
	sha := p.Values["--expected-sha"]
	if p.Definition.Path[0] == "job" {
		id, _ := downloadID(p.Positionals[0])
		pipeline, _ := downloadID(p.Values["--pipeline-id"])
		before, err := readDownloadJob(ctx, client, project, id, pipeline, p.Values["--expected-ref"], sha)
		if err != nil {
			return out, err
		}
		if tx == nil {
			out.data = map[string]any{"artifacts": before}
			return out, nil
		}
		if before.Size > safedownload.MaxArchiveBytes {
			return out, downloadSafety()
		}
		var body bytes.Buffer
		response, err := client.Stream(ctx, productnative.Request{Method: "GET", Path: fmt.Sprintf("projects/%d/jobs/%d/artifacts", project.ID, id), MaxBytes: before.Size}, &body)
		if err != nil {
			return out, err
		}
		if response.Bytes != before.Size {
			return out, downloadSafety()
		}
		after, err := readDownloadJob(ctx, client, project, id, pipeline, p.Values["--expected-ref"], sha)
		if err != nil {
			return out, err
		}
		current, err := readDownloadProject(ctx, client, p.Values["--repo"])
		if err != nil {
			return out, err
		}
		if before != after || current != project {
			return out, downloadSafety()
		}
		extracted, err := tx.Extract(ctx, body.Bytes())
		if err != nil {
			return out, uxv1.Wrap(uxv1.CodeSafety, "artifact archive is unsafe, corrupt, or exceeds extraction bounds", err)
		}
		digest := sha256.Sum256(body.Bytes())
		receipt := downloadReceipt{Kind: "job_artifacts", ProjectID: project.ID, Project: project.Path, PipelineID: pipeline, JobID: id, Ref: before.Ref, SHA: sha, Destination: p.Values["--destination"], Bytes: response.Bytes, SHA256: hex.EncodeToString(digest[:]), Checksum: "zip_crc32_and_size;sha256_receipt_only", Files: extracted.Files, ExpandedBytes: extracted.Bytes}
		if err := tx.Commit(ctx); err != nil {
			return out, uxv1.Wrap(uxv1.CodeSafety, "download publication refused; destination changed", err)
		}
		out.data = map[string]any{"download": receipt}
		return out, nil
	}
	return executeReleaseDownload(ctx, client, project, p, tx, out)
}

type releaseIdentity struct {
	Tag    string `json:"tag_name"`
	Commit struct {
		ID string `json:"id"`
	} `json:"commit"`
	Links struct {
		Self string `json:"self"`
	} `json:"_links"`
}
type releaseDownloadLink struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	URL  string `json:"url"`
}
type downloadPackage struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Version string `json:"version"`
	Type    string `json:"package_type"`
	Status  string `json:"status"`
}
type downloadPackageFile struct {
	ID        int64  `json:"id"`
	PackageID int64  `json:"package_id"`
	Name      string `json:"file_name"`
	Size      int64  `json:"size"`
	SHA256    string `json:"file_sha256"`
}
type releaseDownloadPlan struct {
	Link                    releaseDownloadLink
	Route                   string
	PackageID, FileID, Size int64
	Digest                  string
	Job                     artifactMetadata
}

func downloadCatalog[T any](ctx context.Context, c *productnative.Client, route string, query url.Values) ([]T, error) {
	if query == nil {
		query = url.Values{}
	}
	var all []T
	for page := 1; page <= limits.MaxPages; page++ {
		query.Set("page", strconv.Itoa(page))
		query.Set("per_page", "100")
		var items []T
		response, err := downloadJSON(ctx, c, route, query, &items)
		if err != nil {
			return nil, err
		}
		if len(items) > 100 {
			return nil, downloadSafety()
		}
		all = append(all, items...)
		next := response.Header.Get("X-Next-Page")
		if current := response.Header.Get("X-Page"); current != "" && current != strconv.Itoa(page) {
			return nil, downloadSafety()
		}
		if next != "" && next != strconv.Itoa(page+1) {
			return nil, downloadSafety()
		}
		_, declared := response.Header["X-Next-Page"]
		if next == "" && (declared || len(items) < 100) {
			return all, nil
		}
	}
	return nil, uxv1.NewError(uxv1.CodeSafety, "download selection exceeds the complete catalog page bound")
}

func selectReleaseDownload(ctx context.Context, c *productnative.Client, project downloadProject, p Parsed) (releaseDownloadPlan, error) {
	var plan releaseDownloadPlan
	tag := p.Positionals[0]
	base := fmt.Sprintf("projects/%d", project.ID)
	releaseRoute := base + "/releases/" + url.PathEscape(tag)
	var release releaseIdentity
	if _, err := downloadJSON(ctx, c, releaseRoute, nil, &release); err != nil {
		return plan, err
	}
	if release.Tag != tag || release.Commit.ID != p.Values["--expected-sha"] || release.Links.Self != project.URL+"/-/releases/"+url.PathEscape(tag) {
		return plan, downloadSafety()
	}
	links, err := downloadCatalog[releaseDownloadLink](ctx, c, releaseRoute+"/assets/links", nil)
	if err != nil {
		return plan, err
	}
	id, _ := downloadID(p.Values["--asset-id"])
	seen := map[int64]bool{}
	matches := 0
	for _, link := range links {
		if link.ID <= 0 || seen[link.ID] {
			return plan, downloadSafety()
		}
		seen[link.ID] = true
		if link.ID == id || link.Name == p.Values["--asset-name"] {
			if link.ID != id || link.Name != p.Values["--asset-name"] {
				return plan, downloadSafety()
			}
			matches++
			plan.Link = link
		}
	}
	if matches != 1 {
		return plan, downloadSafety()
	}
	u, err := url.Parse(plan.Link.URL)
	if err != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return plan, downloadSafety()
	}
	api := c.Host().Authority
	prefix := strings.TrimSuffix(api.API.EscapedPath(), "/") + "/projects/"
	if api.SameAPIOrigin(u) && strings.HasPrefix(u.EscapedPath(), prefix) {
		parts := strings.Split(strings.TrimPrefix(u.EscapedPath(), prefix), "/")
		if len(parts) != 6 || parts[1] != "packages" || parts[2] != "generic" {
			return plan, downloadSafety()
		}
		for i := range parts {
			value, err := url.PathUnescape(parts[i])
			if err != nil {
				return plan, downloadSafety()
			}
			parts[i] = value
		}
		if parts[0] != strconv.FormatInt(project.ID, 10) && parts[0] != project.Path {
			return plan, downloadSafety()
		}
		for _, part := range parts[3:] {
			if !safedownload.ValidFileName(part) {
				return plan, downloadSafety()
			}
		}
		packages, err := downloadCatalog[downloadPackage](ctx, c, base+"/packages", url.Values{"package_type": {"generic"}, "package_name": {parts[3]}, "package_version": {parts[4]}, "status": {"default"}})
		if err != nil {
			return plan, err
		}
		matches = 0
		seen = map[int64]bool{}
		for _, pkg := range packages {
			if pkg.ID <= 0 || seen[pkg.ID] {
				return plan, downloadSafety()
			}
			seen[pkg.ID] = true
			if pkg.Name == parts[3] && pkg.Version == parts[4] && pkg.Type == "generic" && pkg.Status == "default" {
				plan.PackageID = pkg.ID
				matches++
			}
		}
		if matches != 1 {
			return plan, downloadSafety()
		}
		files, err := downloadCatalog[downloadPackageFile](ctx, c, fmt.Sprintf("%s/packages/%d/package_files", base, plan.PackageID), nil)
		if err != nil {
			return plan, err
		}
		matches = 0
		seen = map[int64]bool{}
		for _, file := range files {
			if file.ID <= 0 || seen[file.ID] || file.PackageID != plan.PackageID {
				return plan, downloadSafety()
			}
			seen[file.ID] = true
			if file.Name == parts[5] {
				plan.FileID = file.ID
				plan.Size = file.Size
				plan.Digest = file.SHA256
				matches++
			}
		}
		if matches != 1 || plan.Size < 0 || plan.Size > safedownload.MaxArchiveBytes || len(plan.Digest) != 64 || !validMergeSHA(plan.Digest) {
			return plan, downloadSafety()
		}
		plan.Route = base + "/packages/generic/" + url.PathEscape(parts[3]) + "/" + url.PathEscape(parts[4]) + "/" + url.PathEscape(parts[5])
		return plan, nil
	}
	// A release can link directly to a job-owned raw artifact. Translate only
	// this pinned, exact project-web route; never delegate an arbitrary URL.
	webPrefix := project.URL + "/-/jobs/"
	if strings.HasPrefix(plan.Link.URL, webPrefix) {
		parts := strings.SplitN(strings.TrimPrefix(plan.Link.URL, webPrefix), "/artifacts/raw/", 2)
		if len(parts) != 2 {
			return plan, downloadSafety()
		}
		jobID, err := downloadID(parts[0])
		if err != nil {
			return plan, downloadSafety()
		}
		file, err := url.PathUnescape(parts[1])
		if err != nil || !safedownload.ValidRelativePath(file) {
			return plan, downloadSafety()
		}
		job, err := readDownloadJob(ctx, c, project, jobID, 0, "", p.Values["--expected-sha"])
		if err != nil {
			return plan, err
		}
		plan.Job = job
		plan.Size = -1
		escaped := strings.Split(file, "/")
		for i := range escaped {
			escaped[i] = url.PathEscape(escaped[i])
		}
		plan.Route = fmt.Sprintf("%s/jobs/%d/artifacts/%s", base, jobID, strings.Join(escaped, "/"))
		return plan, nil
	}
	return plan, uxv1.NewError(uxv1.CodeSafety, "release link is not an exact project-owned generic package or raw job artifact; external and redirect destinations are not authorized")
}

func executeReleaseDownload(ctx context.Context, c *productnative.Client, project downloadProject, p Parsed, tx *safedownload.Transaction, out commandOutput) (commandOutput, error) {
	before, err := selectReleaseDownload(ctx, c, project, p)
	if err != nil {
		return out, err
	}
	bound := int64(safedownload.MaxArchiveBytes)
	if before.Size > 0 {
		bound = before.Size
	}
	var body bytes.Buffer
	response, err := c.Stream(ctx, productnative.Request{Method: "GET", Path: before.Route, MaxBytes: bound}, &body)
	if err != nil {
		return out, err
	}
	digest := sha256.Sum256(body.Bytes())
	hash := hex.EncodeToString(digest[:])
	if before.Size >= 0 && (response.Bytes != before.Size || hash != before.Digest) {
		return out, downloadSafety()
	}
	after, err := selectReleaseDownload(ctx, c, project, p)
	if err != nil {
		return out, err
	}
	current, err := readDownloadProject(ctx, c, project.Path)
	if err != nil {
		return out, err
	}
	if before != after || current != project {
		return out, downloadSafety()
	}
	if err := tx.Write(ctx, before.Link.Name, bytes.NewReader(body.Bytes()), response.Bytes); err != nil {
		return out, uxv1.Wrap(uxv1.CodeSafety, "release asset staging failed", err)
	}
	checksum := "provider_sha256_and_size"
	if before.Size < 0 {
		checksum = "sha256_receipt_only"
	}
	receipt := downloadReceipt{Kind: "release_asset", ProjectID: project.ID, Project: project.Path, PipelineID: before.Job.PipelineID, JobID: before.Job.JobID, SHA: p.Values["--expected-sha"], Tag: p.Positionals[0], AssetID: before.Link.ID, AssetName: before.Link.Name, PackageID: before.PackageID, PackageFileID: before.FileID, Destination: p.Values["--destination"], Bytes: response.Bytes, SHA256: hash, Checksum: checksum, Files: 1, ExpandedBytes: response.Bytes}
	if err := tx.Commit(ctx); err != nil {
		return out, uxv1.Wrap(uxv1.CodeSafety, "release publication refused; destination changed", err)
	}
	out.data = map[string]any{"download": receipt}
	return out, nil
}
