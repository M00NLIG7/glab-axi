// Package output emits bounded, deterministic JSON or TOON envelopes.
package output

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	v1 "glab-axi/internal/contract/v1"
	"glab-axi/internal/limits"
)

type Format string

const (
	TOON Format = "toon"
	JSON Format = "json"
)

func Write(w io.Writer, format Format, envelope v1.Envelope) error {
	return WriteValue(w, format, envelope)
}

// WriteValue emits either versioned envelope type through the same bounded,
// deterministic encoder. Keeping v1 on this exact path preserves its bytes.
func WriteValue(w io.Writer, format Format, value any) error {
	var data []byte
	var err error
	switch format {
	case JSON:
		data, err = json.Marshal(value)
	case TOON:
		data, err = marshalTOON(value)
	default:
		return v1.NewError(v1.CodeValidation, "format must be toon or json")
	}
	if err != nil {
		return v1.Wrap(v1.CodeInternal, "cannot encode output", err)
	}
	if len(data) > limits.MaxOperationBytes {
		return v1.NewError(v1.CodeUpstream, "output exceeds the operation limit")
	}
	data = append(data, '\n')
	_, err = w.Write(data)
	return err
}

func marshalTOON(value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var generic any
	if err := decoder.Decode(&generic); err != nil {
		return nil, err
	}
	var out strings.Builder
	if err := writeMap(&out, generic.(map[string]any), 0); err != nil {
		return nil, err
	}
	return []byte(strings.TrimSuffix(out.String(), "\n")), nil
}

func writeMap(out *strings.Builder, value map[string]any, indent int) error {
	keys := make([]string, 0, len(value))
	for key := range value {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if indent == 0 {
		priority := map[string]int{"schema": 0, "ok": 1, "data": 2, "error": 3, "help": 4, "meta": 5}
		sort.SliceStable(keys, func(i, j int) bool {
			left, leftKnown := priority[keys[i]]
			right, rightKnown := priority[keys[j]]
			if leftKnown != rightKnown {
				return leftKnown
			}
			if leftKnown {
				return left < right
			}
			return keys[i] < keys[j]
		})
	}
	for _, key := range keys {
		item := value[key]
		prefix(out, indent)
		switch typed := item.(type) {
		case map[string]any:
			out.WriteString(key)
			out.WriteString(":\n")
			if err := writeMap(out, typed, indent+2); err != nil {
				return err
			}
		case []any:
			if tableColumns, ok := tableShape(typed); ok {
				out.WriteString(fmt.Sprintf("%s[%d]{%s}:\n", key, len(typed), strings.Join(tableColumns, ",")))
				for _, row := range typed {
					prefix(out, indent+2)
					m := row.(map[string]any)
					for i, column := range tableColumns {
						if i > 0 {
							out.WriteByte(',')
						}
						out.WriteString(scalar(m[column]))
					}
					out.WriteByte('\n')
				}
			} else {
				out.WriteString(fmt.Sprintf("%s[%d]:\n", key, len(typed)))
				for _, element := range typed {
					prefix(out, indent+2)
					out.WriteString("- ")
					switch nested := element.(type) {
					case map[string]any:
						out.WriteByte('\n')
						if err := writeMap(out, nested, indent+4); err != nil {
							return err
						}
					default:
						out.WriteString(scalar(nested))
						out.WriteByte('\n')
					}
				}
			}
		default:
			out.WriteString(key)
			out.WriteString(": ")
			out.WriteString(scalar(typed))
			out.WriteByte('\n')
		}
	}
	return nil
}

func tableShape(values []any) ([]string, bool) {
	if len(values) == 0 {
		return nil, false
	}
	columnSet := map[string]bool{}
	for _, row := range values {
		m, ok := row.(map[string]any)
		if !ok {
			return nil, false
		}
		for key, value := range m {
			if !isScalar(value) {
				return nil, false
			}
			columnSet[key] = true
		}
	}
	columns := make([]string, 0, len(columnSet))
	for column := range columnSet {
		columns = append(columns, column)
	}
	sort.Strings(columns)
	return columns, true
}

func scalar(value any) string {
	switch typed := value.(type) {
	case nil:
		return "null"
	case bool:
		return strconv.FormatBool(typed)
	case json.Number:
		return typed.String()
	case string:
		encoded, _ := json.Marshal(typed)
		return string(encoded)
	default:
		encoded, _ := json.Marshal(typed)
		return string(encoded)
	}
}

func isScalar(value any) bool {
	switch value.(type) {
	case nil, bool, json.Number, string:
		return true
	default:
		return false
	}
}

func prefix(out *strings.Builder, indent int) {
	out.WriteString(strings.Repeat(" ", indent))
}
