package gitlab

import (
	"bytes"
	"testing"

	"gl-axi/internal/limits"
)

func TestResponseBodyLimitBoundary(t *testing.T) {
	exact := bytes.Repeat([]byte{'x'}, limits.MaxJSONPageBytes)
	data, tooLarge, err := readBounded(bytes.NewReader(exact), limits.MaxJSONPageBytes)
	if err != nil || tooLarge || len(data) != limits.MaxJSONPageBytes {
		t.Fatalf("exact boundary: len=%d tooLarge=%v err=%v", len(data), tooLarge, err)
	}
	over := append(exact, 'x')
	data, tooLarge, err = readBounded(bytes.NewReader(over), limits.MaxJSONPageBytes)
	if err != nil || !tooLarge || len(data) != limits.MaxJSONPageBytes {
		t.Fatalf("over boundary: len=%d tooLarge=%v err=%v", len(data), tooLarge, err)
	}
}

func TestTraceTailLimitBoundary(t *testing.T) {
	exact := bytes.Repeat([]byte{'a'}, limits.MaxTraceBytes)
	tail := newTailBuffer(limits.MaxTraceBytes)
	_, _ = tail.Write(exact)
	if tail.truncated || !bytes.Equal(tail.Bytes(), exact) {
		t.Fatal("exact trace boundary was truncated")
	}
	tail = newTailBuffer(limits.MaxTraceBytes)
	over := append([]byte{'z'}, exact...)
	_, _ = tail.Write(over)
	if !tail.truncated || len(tail.Bytes()) != limits.MaxTraceBytes || tail.Bytes()[0] != 'a' {
		t.Fatal("trace boundary+1 did not retain an exact bounded tail")
	}
}
