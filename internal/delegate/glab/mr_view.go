package glab

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"

	"gl-axi/internal/contract/uxv1"
)

// normalizeMRViewResponse converts the pinned official client's source-head
// representation into the REST-shaped field consumed by product normalizers.
// Official glab can carry the exact source head in diff_refs.head_sha, while
// fixed API reads and mutation responses carry it in sha. Both forms remain
// accepted, but conflicting or malformed proofs fail closed.
func normalizeMRViewResponse(body []byte) ([]byte, error) {
	object, err := decodeUniqueJSONObject(body)
	if err != nil {
		return nil, err
	}

	topHead, _, err := optionalJSONString(object, "sha")
	if err != nil {
		return nil, err
	}
	if topHead != "" && !validResponseSHA(topHead) {
		return nil, uxv1.NewError(uxv1.CodeUpstream, "official glab returned an invalid merge request head")
	}

	diffHead := ""
	if raw, exists := object["diff_refs"]; exists && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		diffRefs, decodeErr := decodeUniqueJSONObject(raw)
		if decodeErr != nil {
			return nil, uxv1.Wrap(uxv1.CodeUpstream, "official glab returned malformed merge request diff identity", decodeErr)
		}
		diffHead, _, err = optionalJSONString(diffRefs, "head_sha")
		if err != nil {
			return nil, err
		}
		if diffHead != "" && !validResponseSHA(diffHead) {
			return nil, uxv1.NewError(uxv1.CodeUpstream, "official glab returned an invalid merge request diff head")
		}
	}
	if topHead != "" && diffHead != "" && topHead != diffHead {
		return nil, uxv1.NewError(uxv1.CodeSafety, "official glab returned conflicting merge request head identities")
	}
	if topHead == "" && diffHead != "" {
		encoded, _ := json.Marshal(diffHead)
		object["sha"] = encoded
	}

	projectID, hasProjectID, err := optionalPositiveJSONInteger(object, "project_id")
	if err != nil {
		return nil, err
	}
	targetProjectID, hasTargetProjectID, err := optionalPositiveJSONInteger(object, "target_project_id")
	if err != nil {
		return nil, err
	}
	if hasProjectID && hasTargetProjectID && projectID != targetProjectID {
		return nil, uxv1.NewError(uxv1.CodeSafety, "official glab returned conflicting merge request project identities")
	}

	normalized, err := json.Marshal(object)
	if err != nil {
		return nil, uxv1.Wrap(uxv1.CodeInternal, "cannot normalize official glab merge request view", err)
	}
	return normalized, nil
}

func decodeUniqueJSONObject(body []byte) (map[string]json.RawMessage, error) {
	if len(body) == 0 {
		return nil, uxv1.NewError(uxv1.CodeUpstream, "official glab returned malformed JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return nil, uxv1.NewError(uxv1.CodeUpstream, "official glab returned malformed JSON object")
	}
	object := make(map[string]json.RawMessage)
	for decoder.More() {
		token, tokenErr := decoder.Token()
		key, ok := token.(string)
		if tokenErr != nil || !ok {
			return nil, uxv1.NewError(uxv1.CodeUpstream, "official glab returned malformed JSON object")
		}
		if _, duplicate := object[key]; duplicate {
			return nil, uxv1.NewError(uxv1.CodeUpstream, "official glab returned duplicate JSON fields")
		}
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return nil, uxv1.Wrap(uxv1.CodeUpstream, "official glab returned malformed JSON", err)
		}
		object[key] = raw
	}
	closing, err := decoder.Token()
	if err != nil || closing != json.Delim('}') {
		return nil, uxv1.NewError(uxv1.CodeUpstream, "official glab returned malformed JSON object")
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, uxv1.NewError(uxv1.CodeUpstream, "official glab returned trailing data")
	}
	return object, nil
}

func optionalJSONString(object map[string]json.RawMessage, key string) (string, bool, error) {
	raw, exists := object[key]
	if !exists || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return "", exists, nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", true, uxv1.NewError(uxv1.CodeUpstream, "official glab returned malformed merge request identity")
	}
	return value, true, nil
}

func optionalPositiveJSONInteger(object map[string]json.RawMessage, key string) (int64, bool, error) {
	raw, exists := object[key]
	if !exists || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return 0, false, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return 0, true, uxv1.NewError(uxv1.CodeUpstream, "official glab returned malformed merge request project identity")
	}
	number, ok := decoded.(json.Number)
	if !ok {
		return 0, true, uxv1.NewError(uxv1.CodeUpstream, "official glab returned malformed merge request project identity")
	}
	value, err := strconv.ParseInt(number.String(), 10, 64)
	if err != nil || value < 1 || strconv.FormatInt(value, 10) != number.String() {
		return 0, true, uxv1.NewError(uxv1.CodeUpstream, "official glab returned invalid merge request project identity")
	}
	return value, true, nil
}

func validResponseSHA(value string) bool {
	if len(value) != 40 && len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	decoded, err := hex.DecodeString(value)
	if err != nil {
		return false
	}
	for _, item := range decoded {
		if item != 0 {
			return true
		}
	}
	return false
}
