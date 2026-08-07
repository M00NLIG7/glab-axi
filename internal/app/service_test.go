package app

import (
	"strings"
	"testing"

	"glab-axi/internal/limits"
)

func TestMergeRequestInputLimitsBoundary(t *testing.T) {
	if err := validateMRInput("feature", "main", strings.Repeat("t", limits.MaxTitleBytes), strings.Repeat("d", limits.MaxDescriptionBytes)); err != nil {
		t.Fatalf("exact input boundary rejected: %v", err)
	}
	if err := validateMRInput("feature", "main", strings.Repeat("t", limits.MaxTitleBytes+1), ""); err == nil {
		t.Fatal("title boundary+1 accepted")
	}
	if err := validateMRInput("feature", "main", "title", strings.Repeat("d", limits.MaxDescriptionBytes+1)); err == nil {
		t.Fatal("description boundary+1 accepted")
	}
	if err := validateMRInput("feature", "main", "line\nbreak", ""); err == nil {
		t.Fatal("multiline title accepted")
	}
}
