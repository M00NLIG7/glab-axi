package product

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"gl-axi/internal/contract/uxv1"
	"gl-axi/internal/delegate/glab"
	"gl-axi/internal/limits"
	"gl-axi/internal/output"
)

type upstreamMRDiscussionIdentity struct {
	ID              int64  `json:"id"`
	IID             int64  `json:"iid"`
	ProjectID       int64  `json:"project_id"`
	TargetProjectID int64  `json:"target_project_id"`
	WebURL          string `json:"web_url"`
}

type upstreamDiscussionUser struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	Name     string `json:"name"`
}

type upstreamDiscussionLine struct {
	LineCode string `json:"line_code"`
	Type     string `json:"type"`
	OldLine  *int64 `json:"old_line"`
	NewLine  *int64 `json:"new_line"`
}

type upstreamDiscussionLineRange struct {
	Start *upstreamDiscussionLine `json:"start"`
	End   *upstreamDiscussionLine `json:"end"`
}

type upstreamDiscussionPosition struct {
	BaseSHA      string                       `json:"base_sha"`
	StartSHA     string                       `json:"start_sha"`
	HeadSHA      string                       `json:"head_sha"`
	OldPath      string                       `json:"old_path"`
	NewPath      string                       `json:"new_path"`
	PositionType string                       `json:"position_type"`
	OldLine      *int64                       `json:"old_line"`
	NewLine      *int64                       `json:"new_line"`
	LineRange    *upstreamDiscussionLineRange `json:"line_range"`
}

type upstreamDiscussionNote struct {
	ID           int64                       `json:"id"`
	Type         string                      `json:"type"`
	Body         *string                     `json:"body"`
	Author       *upstreamDiscussionUser     `json:"author"`
	CreatedAt    *time.Time                  `json:"created_at"`
	UpdatedAt    *time.Time                  `json:"updated_at"`
	System       *bool                       `json:"system"`
	NoteableID   int64                       `json:"noteable_id"`
	NoteableType string                      `json:"noteable_type"`
	ProjectID    int64                       `json:"project_id"`
	NoteableIID  *int64                      `json:"noteable_iid"`
	Resolvable   *bool                       `json:"resolvable"`
	Resolved     *bool                       `json:"resolved"`
	ResolvedBy   *upstreamDiscussionUser     `json:"resolved_by"`
	ResolvedAt   *time.Time                  `json:"resolved_at"`
	Position     *upstreamDiscussionPosition `json:"position"`
}

type upstreamDiscussion struct {
	ID             string                   `json:"id"`
	IndividualNote *bool                    `json:"individual_note"`
	Notes          []upstreamDiscussionNote `json:"notes"`
}

// MRDiscussionIdentity binds every returned thread to one canonical merge
// request. Project and merge-request global IDs are provider identities, while
// the IID and URL are the human-facing project-local identity.
type MRDiscussionIdentity struct {
	ID        int64  `json:"id"`
	IID       int64  `json:"iid"`
	ProjectID int64  `json:"project_id"`
	WebURL    string `json:"web_url"`
}

type DiscussionUser struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	Name     string `json:"name,omitempty"`
}

type DiscussionLine struct {
	Type     string `json:"type,omitempty"`
	OldLine  *int64 `json:"old_line,omitempty"`
	NewLine  *int64 `json:"new_line,omitempty"`
	LineCode string `json:"line_code,omitempty"`
}

type DiscussionLineRange struct {
	Start DiscussionLine `json:"start"`
	End   DiscussionLine `json:"end"`
}

type DiscussionPosition struct {
	PositionType string               `json:"position_type"`
	BaseSHA      string               `json:"base_sha,omitempty"`
	StartSHA     string               `json:"start_sha,omitempty"`
	HeadSHA      string               `json:"head_sha,omitempty"`
	OldPath      string               `json:"old_path,omitempty"`
	NewPath      string               `json:"new_path,omitempty"`
	OldLine      *int64               `json:"old_line,omitempty"`
	NewLine      *int64               `json:"new_line,omitempty"`
	LineRange    *DiscussionLineRange `json:"line_range,omitempty"`
}

type DiscussionNote struct {
	ID         int64               `json:"id"`
	Type       string              `json:"type,omitempty"`
	Author     DiscussionUser      `json:"author"`
	CreatedAt  time.Time           `json:"created_at"`
	UpdatedAt  time.Time           `json:"updated_at"`
	Body       string              `json:"body"`
	System     bool                `json:"system"`
	Resolvable bool                `json:"resolvable"`
	Resolved   bool                `json:"resolved"`
	ResolvedBy *DiscussionUser     `json:"resolved_by,omitempty"`
	ResolvedAt *time.Time          `json:"resolved_at,omitempty"`
	Position   *DiscussionPosition `json:"position,omitempty"`
}

type MRDiscussion struct {
	ID             string           `json:"id"`
	IndividualNote bool             `json:"individual_note"`
	Notes          []DiscussionNote `json:"notes"`
}

type discussionPageNormalizer struct {
	mrID              int64
	iid               int64
	projectID         int64
	seenDiscussionIDs map[string]bool
	seenNoteIDs       map[int64]bool
}

func executeMRDiscussions(ctx context.Context, client delegateClient, target Target, parsed Parsed, meta uxv1.Meta) (commandOutput, error) {
	iid, err := positivePosition(parsed, "merge request IID")
	if err != nil {
		return commandOutput{meta: meta}, err
	}

	response, err := client.Do(ctx, glab.Request{Operation: glab.OpMRView, Host: target.Host, Repo: target.Repo, IID: iid})
	meta.UpstreamVersion = response.UpstreamVersion
	if err != nil {
		return commandOutput{meta: meta}, err
	}
	mr, err := normalizeMRDiscussionIdentity(response.Body, target, iid)
	if err != nil {
		return commandOutput{meta: meta}, err
	}

	normalizer := &discussionPageNormalizer{
		mrID:              mr.ID,
		iid:               mr.IID,
		projectID:         mr.ProjectID,
		seenDiscussionIDs: make(map[string]bool),
		seenNoteIDs:       make(map[int64]bool),
	}
	discussions, state, err := fetchList(ctx, client, glab.Request{
		Operation: glab.OpMRDiscussions,
		Host:      target.Host,
		Repo:      target.Repo,
		IID:       iid,
	}, parsed.Limit, normalizer.normalize)
	meta = mergeMeta(meta, state)
	if err != nil {
		return commandOutput{meta: meta}, err
	}

	discussions, recordTruncated, bodyTruncated, err := boundDiscussionOutput(discussions)
	if err != nil {
		return commandOutput{meta: meta}, err
	}
	if recordTruncated {
		meta.Complete = false
		meta.Truncated = true
		if meta.Reason == "" || meta.Reason == "field_limit" {
			meta.Reason = "nested_record_limit"
		}
	}
	if bodyTruncated {
		meta.Truncated = true
		if meta.Reason == "" {
			meta.Reason = "field_limit"
		}
	}
	data := map[string]any{"mr": mr, "discussions": discussions}
	if err := output.WriteValue(io.Discard, parsed.Format, uxv1.Success(data, meta)); err != nil {
		meta.Complete = false
		meta.Truncated = true
		meta.Reason = "operation_limit"
		return commandOutput{meta: meta}, uxv1.NewError(uxv1.CodeUpstream, "normalized discussion output exceeded the safety limit")
	}
	return commandOutput{data: data, meta: meta}, nil
}

func normalizeMRDiscussionIdentity(body []byte, target Target, expectedIID int64) (MRDiscussionIdentity, error) {
	if err := validateDiscussionJSON(body, '{'); err != nil {
		return MRDiscussionIdentity{}, err
	}
	var source upstreamMRDiscussionIdentity
	if err := decodeStrict(body, &source); err != nil {
		return MRDiscussionIdentity{}, err
	}
	if source.ID < 1 || source.IID < 1 || source.ProjectID < 1 {
		return MRDiscussionIdentity{}, malformed("merge request discussion identity")
	}
	if source.IID != expectedIID {
		return MRDiscussionIdentity{}, uxv1.NewError(uxv1.CodeSafety, "official glab returned a different merge request than requested")
	}
	if source.TargetProjectID < 0 {
		return MRDiscussionIdentity{}, malformed("merge request target project identity")
	}
	if source.TargetProjectID != 0 && source.TargetProjectID != source.ProjectID {
		return MRDiscussionIdentity{}, uxv1.NewError(uxv1.CodeSafety, "official glab returned conflicting merge request project identities")
	}
	web, err := authorityRepoURL(source.WebURL, target.Host, target.Repo, true)
	if err != nil {
		return MRDiscussionIdentity{}, err
	}
	parsed, _ := url.Parse(web)
	expectedSuffix := (&url.URL{Path: "/" + target.Repo + "/-/merge_requests/" + strconv.FormatInt(expectedIID, 10)}).EscapedPath()
	if parsed.EscapedPath() != expectedSuffix && !strings.HasSuffix(parsed.EscapedPath(), expectedSuffix) {
		return MRDiscussionIdentity{}, uxv1.NewError(uxv1.CodeSafety, "official glab returned a URL for a different merge request")
	}
	return MRDiscussionIdentity{ID: source.ID, IID: source.IID, ProjectID: source.ProjectID, WebURL: web}, nil
}

func (n *discussionPageNormalizer) normalize(body []byte) ([]MRDiscussion, bool, error) {
	if len(body) > limits.MaxJSONPageBytes {
		return nil, false, uxv1.NewError(uxv1.CodeUpstream, "official glab output exceeded the safety limit")
	}
	if err := validateDiscussionJSON(body, '['); err != nil {
		return nil, false, err
	}
	var source []upstreamDiscussion
	if err := decodeStrict(body, &source); err != nil {
		return nil, false, err
	}
	out := make([]MRDiscussion, 0, len(source))
	truncated := false
	for _, item := range source {
		discussion, cut, err := n.normalizeDiscussion(item)
		if err != nil {
			return nil, false, err
		}
		truncated = truncated || cut
		out = append(out, discussion)
	}
	return out, truncated, nil
}

func (n *discussionPageNormalizer) normalizeDiscussion(source upstreamDiscussion) (MRDiscussion, bool, error) {
	id, err := strictDiscussionIdentifier(source.ID, "discussion identity", limits.MaxDiscussionIdentifierBytes)
	if err != nil {
		return MRDiscussion{}, false, err
	}
	if n.seenDiscussionIDs[id] {
		return MRDiscussion{}, false, malformed("duplicate discussion identity")
	}
	n.seenDiscussionIDs[id] = true
	if source.IndividualNote == nil || len(source.Notes) == 0 {
		return MRDiscussion{}, false, malformed("discussion record")
	}
	if *source.IndividualNote && len(source.Notes) != 1 {
		return MRDiscussion{}, false, malformed("individual discussion record")
	}

	notes := make([]DiscussionNote, 0, len(source.Notes))
	truncated := false
	for _, item := range source.Notes {
		note, cut, err := n.normalizeNote(item)
		if err != nil {
			return MRDiscussion{}, false, err
		}
		truncated = truncated || cut
		notes = append(notes, note)
	}
	return MRDiscussion{ID: id, IndividualNote: *source.IndividualNote, Notes: notes}, truncated, nil
}

func (n *discussionPageNormalizer) normalizeNote(source upstreamDiscussionNote) (DiscussionNote, bool, error) {
	if source.ID < 1 || n.seenNoteIDs[source.ID] {
		return DiscussionNote{}, false, malformed("discussion note identity")
	}
	n.seenNoteIDs[source.ID] = true
	if source.Body == nil || *source.Body == "" || !utf8.ValidString(*source.Body) || strings.ContainsRune(*source.Body, '\x00') {
		return DiscussionNote{}, false, malformed("discussion note body")
	}
	if source.Author == nil || source.CreatedAt == nil || source.UpdatedAt == nil || source.CreatedAt.IsZero() || source.UpdatedAt.IsZero() {
		return DiscussionNote{}, false, malformed("discussion note attribution")
	}
	if source.System == nil || source.Resolvable == nil || source.Resolved == nil {
		return DiscussionNote{}, false, malformed("discussion note flags")
	}
	if source.NoteableID < 1 || source.ProjectID < 1 || source.NoteableType == "" || source.NoteableIID != nil && *source.NoteableIID < 1 {
		return DiscussionNote{}, false, malformed("discussion note resource identity")
	}
	if source.NoteableID != n.mrID || source.ProjectID != n.projectID || source.NoteableType != "MergeRequest" || source.NoteableIID != nil && *source.NoteableIID != n.iid {
		return DiscussionNote{}, false, uxv1.NewError(uxv1.CodeSafety, "official glab returned a discussion note outside the selected merge request")
	}
	if *source.Resolved && !*source.Resolvable || !*source.Resolved && (source.ResolvedBy != nil || source.ResolvedAt != nil) {
		return DiscussionNote{}, false, malformed("discussion note resolution")
	}

	author, authorCut, err := normalizeDiscussionUser(source.Author, "discussion note author")
	if err != nil {
		return DiscussionNote{}, false, err
	}
	noteType, err := optionalDiscussionEnum(source.Type, "discussion note type")
	if err != nil {
		return DiscussionNote{}, false, err
	}
	position, positionCut, err := normalizeDiscussionPosition(source.Position)
	if err != nil {
		return DiscussionNote{}, false, err
	}
	var resolvedBy *DiscussionUser
	resolverCut := false
	if source.ResolvedBy != nil {
		resolved, cut, err := normalizeDiscussionUser(source.ResolvedBy, "discussion note resolver")
		if err != nil {
			return DiscussionNote{}, false, err
		}
		resolvedBy, resolverCut = &resolved, cut
	}
	return DiscussionNote{
		ID:         source.ID,
		Type:       noteType,
		Author:     author,
		CreatedAt:  *source.CreatedAt,
		UpdatedAt:  *source.UpdatedAt,
		Body:       *source.Body,
		System:     *source.System,
		Resolvable: *source.Resolvable,
		Resolved:   *source.Resolved,
		ResolvedBy: resolvedBy,
		ResolvedAt: source.ResolvedAt,
		Position:   position,
	}, authorCut || resolverCut || positionCut, nil
}

func normalizeDiscussionUser(source *upstreamDiscussionUser, label string) (DiscussionUser, bool, error) {
	if source == nil || source.ID < 1 {
		return DiscussionUser{}, false, malformed(label)
	}
	username, err := strictDiscussionIdentifier(source.Username, label+" username", limits.MaxDiscussionUserFieldBytes)
	if err != nil {
		return DiscussionUser{}, false, err
	}
	name, cut, err := boundedText(source.Name, label+" name", limits.MaxDiscussionUserFieldBytes, false)
	if err != nil {
		return DiscussionUser{}, false, err
	}
	return DiscussionUser{ID: source.ID, Username: username, Name: name}, cut, nil
}

func normalizeDiscussionPosition(source *upstreamDiscussionPosition) (*DiscussionPosition, bool, error) {
	if source == nil {
		return nil, false, nil
	}
	positionType, err := requiredDiscussionEnum(source.PositionType, "discussion position type")
	if err != nil {
		return nil, false, err
	}
	oldPath, oldCut, err := boundedText(source.OldPath, "discussion old path", limits.MaxDiscussionPathBytes, false)
	if err != nil {
		return nil, false, err
	}
	newPath, newCut, err := boundedText(source.NewPath, "discussion new path", limits.MaxDiscussionPathBytes, false)
	if err != nil {
		return nil, false, err
	}
	if oldPath == "" && newPath == "" {
		return nil, false, malformed("discussion position path")
	}
	if err := validateOptionalPositiveLine(source.OldLine); err != nil {
		return nil, false, err
	}
	if err := validateOptionalPositiveLine(source.NewLine); err != nil {
		return nil, false, err
	}

	position := &DiscussionPosition{
		PositionType: positionType,
		OldPath:      oldPath,
		NewPath:      newPath,
		OldLine:      source.OldLine,
		NewLine:      source.NewLine,
	}
	for _, identity := range []struct {
		raw         string
		destination *string
	}{
		{raw: source.BaseSHA, destination: &position.BaseSHA},
		{raw: source.StartSHA, destination: &position.StartSHA},
		{raw: source.HeadSHA, destination: &position.HeadSHA},
	} {
		if identity.raw == "" {
			continue
		}
		if !validMergeSHA(identity.raw) {
			return nil, false, malformed("discussion position commit identity")
		}
		*identity.destination = identity.raw
	}
	truncated := oldCut || newCut
	if source.LineRange != nil {
		if source.LineRange.Start == nil || source.LineRange.End == nil {
			return nil, false, malformed("discussion line range")
		}
		start, startCut, err := normalizeDiscussionLine(*source.LineRange.Start)
		if err != nil {
			return nil, false, err
		}
		end, endCut, err := normalizeDiscussionLine(*source.LineRange.End)
		if err != nil {
			return nil, false, err
		}
		position.LineRange = &DiscussionLineRange{Start: start, End: end}
		truncated = truncated || startCut || endCut
	}
	return position, truncated, nil
}

func normalizeDiscussionLine(source upstreamDiscussionLine) (DiscussionLine, bool, error) {
	if err := validateOptionalPositiveLine(source.OldLine); err != nil {
		return DiscussionLine{}, false, err
	}
	if err := validateOptionalPositiveLine(source.NewLine); err != nil {
		return DiscussionLine{}, false, err
	}
	if source.OldLine == nil && source.NewLine == nil {
		return DiscussionLine{}, false, malformed("discussion line range position")
	}
	lineType, err := optionalDiscussionEnum(source.Type, "discussion line type")
	if err != nil {
		return DiscussionLine{}, false, err
	}
	lineCode, cut, err := boundedDiscussionMetadata(source.LineCode, "discussion line code", limits.MaxDiscussionLineCodeBytes)
	if err != nil {
		return DiscussionLine{}, false, err
	}
	return DiscussionLine{Type: lineType, OldLine: source.OldLine, NewLine: source.NewLine, LineCode: lineCode}, cut, nil
}

// validateDiscussionJSON rejects duplicate fields before typed decoding. This
// matters for identity and boolean policy fields where last-value-wins decoding
// would otherwise make malformed provider output ambiguous.
func validateDiscussionJSON(body []byte, root byte) error {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 || trimmed[0] != root || !utf8.Valid(body) {
		return uxv1.NewError(uxv1.CodeUpstream, "official glab returned malformed discussion JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := consumeUniqueJSONValue(decoder); err != nil {
		return uxv1.NewError(uxv1.CodeUpstream, "official glab returned malformed discussion JSON")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return uxv1.NewError(uxv1.CodeUpstream, "official glab returned malformed discussion JSON")
	}
	return nil
}

func consumeUniqueJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]bool)
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok || seen[key] {
				return uxv1.NewError(uxv1.CodeUpstream, "duplicate discussion JSON field")
			}
			seen[key] = true
			if err := consumeUniqueJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return uxv1.NewError(uxv1.CodeUpstream, "malformed discussion JSON object")
		}
	case '[':
		for decoder.More() {
			if err := consumeUniqueJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return uxv1.NewError(uxv1.CodeUpstream, "malformed discussion JSON array")
		}
	default:
		return uxv1.NewError(uxv1.CodeUpstream, "malformed discussion JSON delimiter")
	}
	return nil
}

func validateOptionalPositiveLine(line *int64) error {
	if line != nil && *line < 1 {
		return malformed("discussion line number")
	}
	return nil
}

func strictDiscussionIdentifier(value, label string, maxBytes int) (string, error) {
	if value == "" || len(value) > maxBytes || !utf8.ValidString(value) || strings.TrimSpace(value) != value || strings.ContainsFunc(value, func(r rune) bool {
		return unicode.IsControl(r) || unicode.Is(unicode.Cf, r)
	}) {
		return "", malformed(label)
	}
	return value, nil
}

func requiredDiscussionEnum(value, label string) (string, error) {
	if value == "" || len(value) > 128 || !utf8.ValidString(value) || strings.TrimSpace(value) != value || strings.ContainsFunc(value, unicode.IsControl) {
		return "", malformed(label)
	}
	return value, nil
}

func optionalDiscussionEnum(value, label string) (string, error) {
	if value == "" {
		return "", nil
	}
	return requiredDiscussionEnum(value, label)
}

func boundedDiscussionMetadata(value, label string, maxBytes int) (string, bool, error) {
	if value == "" {
		return "", false, nil
	}
	if strings.ContainsFunc(value, func(r rune) bool { return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) }) {
		return "", false, malformed(label)
	}
	return boundedText(value, label, maxBytes, false)
}

// boundDiscussionOutput keeps every selected thread and an authoritative prefix
// of its notes. When the nested cap is reached, each thread keeps its first note
// before remaining capacity is shared in provider thread order. The body budget
// is then shared across remaining notes so an early oversized body cannot
// consume all useful conversation context.
func boundDiscussionOutput(discussions []MRDiscussion) ([]MRDiscussion, bool, bool, error) {
	totalNotes := 0
	for _, discussion := range discussions {
		totalNotes += len(discussion.Notes)
	}
	recordTruncated := totalNotes > limits.MaxDiscussionNotes
	if recordTruncated {
		if len(discussions) > limits.MaxDiscussionNotes {
			return nil, false, false, uxv1.NewError(uxv1.CodeUpstream, "official glab returned too many discussion records")
		}
		kept := make([]int, len(discussions))
		for index := range kept {
			kept[index] = 1
		}
		remaining := limits.MaxDiscussionNotes - len(discussions)
		for remaining > 0 {
			progress := false
			for index := range discussions {
				if kept[index] >= len(discussions[index].Notes) {
					continue
				}
				kept[index]++
				remaining--
				progress = true
				if remaining == 0 {
					break
				}
			}
			if !progress {
				break
			}
		}
		for index := range discussions {
			discussions[index].Notes = discussions[index].Notes[:kept[index]]
		}
		totalNotes = limits.MaxDiscussionNotes
	}

	remainingBytes := limits.MaxDiscussionBodiesBytes
	remainingNotes := totalNotes
	bodyTruncated := false
	for discussionIndex := range discussions {
		for noteIndex := range discussions[discussionIndex].Notes {
			allowance := remainingBytes
			if remainingNotes > 0 {
				allowance /= remainingNotes
			}
			allowance = min(allowance, limits.MaxDescriptionBytes)
			body, cut, err := boundedText(discussions[discussionIndex].Notes[noteIndex].Body, "discussion note body", allowance, true)
			if err != nil {
				return nil, false, false, err
			}
			discussions[discussionIndex].Notes[noteIndex].Body = body
			bodyTruncated = bodyTruncated || cut
			remainingBytes -= min(len(body), remainingBytes)
			remainingNotes--
		}
	}
	return discussions, recordTruncated, bodyTruncated, nil
}
