// Package civariable defines the confidential project-variable wire boundary.
// Provider values and descriptions never leave Decode; only exact comparisons
// for a caller-selected key and scope survive, and those are never serialized.
package civariable

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const MaxValueBytes = 10000

var keyPattern = regexp.MustCompile(`^[A-Za-z0-9_]{1,255}$`)

func ValidKey(s string) bool { return keyPattern.MatchString(s) }
func ValidScope(s string) bool {
	return s != "" && len(s) <= 255 && utf8.ValidString(s) && strings.TrimSpace(s) == s && !strings.ContainsFunc(s, func(r rune) bool { return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) })
}
func ValidValue(s string, masked bool) bool {
	if s == "" || len(s) > MaxValueBytes || !utf8.ValidString(s) || strings.ContainsRune(s, 0) {
		return false
	}
	return !masked || utf8.RuneCountInString(s) >= 8 && !strings.ContainsFunc(s, unicode.IsSpace)
}

type Metadata struct {
	Key              string `json:"key"`
	EnvironmentScope string `json:"environment_scope"`
	VariableType     string `json:"variable_type"`
	Masked           bool   `json:"masked"`
	Hidden           bool   `json:"hidden"`
	Protected        bool   `json:"protected"`
	Raw              bool   `json:"raw"`
	Class            string `json:"class"`
}

func (m Metadata) Classification() string {
	switch {
	case m.Hidden:
		return "hidden"
	case m.Masked:
		return "masked"
	case m.Protected:
		return "protected"
	default:
		return "ordinary"
	}
}

type Observation struct {
	Metadata
	MatchesExpected bool `json:"-"`
	MatchesDesired  bool `json:"-"`
}

// Comparison is transient private memory, never argv, JSON, or a receipt.
type Comparison struct {
	Key      string
	Scope    string
	Expected *string `json:"-"`
	Desired  *string `json:"-"`
}

// Decode accepts a list, drops every non-metadata field immediately,
// and refuses absent booleans rather than treating older servers as unhidden.
// The caller owns and clears the raw response buffer, including error paths.
func Decode(body []byte, match Comparison) ([]Observation, error) {
	bad := errors.New("invalid or unavailable CI variable metadata")
	if !utf8.Valid(body) {
		return nil, bad
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	var objects []json.RawMessage
	if decoder.Decode(&objects) != nil || objects == nil {
		return nil, bad
	}
	defer func() {
		for _, o := range objects {
			clear(o)
		}
	}()
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return nil, bad
	}
	if len(objects) > 100 {
		return nil, bad
	}
	out := make([]Observation, 0, len(objects))
	for _, object := range objects {
		// Reject duplicate fields: an ambiguous security flag is not evidence.
		d := json.NewDecoder(bytes.NewReader(object))
		token, err := d.Token()
		if err != nil || token != json.Delim('{') {
			return nil, bad
		}
		fields := map[string]json.RawMessage{}
		cleanup := func() {
			for _, v := range fields {
				clear(v)
			}
		}
		for d.More() {
			token, err = d.Token()
			if err != nil {
				cleanup()
				return nil, bad
			}
			name, ok := token.(string)
			if !ok {
				cleanup()
				return nil, bad
			}
			if _, exists := fields[name]; exists {
				cleanup()
				return nil, bad
			}
			var value json.RawMessage
			if d.Decode(&value) != nil {
				cleanup()
				return nil, bad
			}
			fields[name] = value
		}
		if _, err = d.Token(); err != nil {
			cleanup()
			return nil, bad
		}
		var m Metadata
		valid := true
		for name, dst := range map[string]any{"key": &m.Key, "environment_scope": &m.EnvironmentScope, "variable_type": &m.VariableType, "masked": &m.Masked, "hidden": &m.Hidden, "protected": &m.Protected, "raw": &m.Raw} {
			raw, ok := fields[name]
			if !ok || bytes.Equal(raw, []byte("null")) || json.Unmarshal(raw, dst) != nil {
				valid = false
			}
		}
		if !valid || !ValidKey(m.Key) || !ValidScope(m.EnvironmentScope) || m.VariableType != "env_var" && m.VariableType != "file" || m.Hidden && !m.Masked {
			cleanup()
			return nil, bad
		}
		m.Class = m.Classification()
		observation := Observation{Metadata: m}
		if !m.Hidden && m.Key == match.Key && m.EnvironmentScope == match.Scope {
			var value string
			if raw, ok := fields["value"]; ok && !bytes.Equal(raw, []byte("null")) && json.Unmarshal(raw, &value) == nil {
				if match.Expected != nil {
					observation.MatchesExpected = subtle.ConstantTimeCompare([]byte(value), []byte(*match.Expected)) == 1
				}
				if match.Desired != nil {
					observation.MatchesDesired = subtle.ConstantTimeCompare([]byte(value), []byte(*match.Desired)) == 1
				}
			}
			value = ""
		}
		cleanup()
		out = append(out, observation)
	}
	return out, nil
}
