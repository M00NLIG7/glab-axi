package product

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"testing"

	runtimepkg "gl-axi/internal/runtime"
)

func assertReleaseCatalogUnpublicationReceipt(t *testing.T, r deletionTestReceipt, outcome string) {
	t.Helper()
	if r.CatalogUnpublication == nil || !r.CatalogUnpublication.Acknowledged || r.CatalogUnpublication.Outcome != outcome {
		t.Fatalf("catalog unpublication consent or uncertainty missing: %+v", r.CatalogUnpublication)
	}
	if !slices.Equal(r.IntendedEffects, []string{"delete_release", "may_unpublish_catalog_resource", "retain_tag"}) {
		t.Fatalf("misleading release effects: %v", r.IntendedEffects)
	}
}

func TestReleaseDeletionCatalogAcknowledgmentRefusals(t *testing.T) {
	item := deletionCases()[2]
	base := item.args()
	withoutAck := deletionReplaceFlag(base, "--acknowledge-catalog-unpublication", "", true)
	for name, args := range map[string][]string{
		"missing":                      withoutAck,
		"sibling":                      deletionReplaceFlag(base, "--acknowledge-catalog-unpublication", deletionTestWeb+"/group/project/-/releases/v2.0", false),
		"project-url":                  deletionReplaceFlag(base, "--acknowledge-catalog-unpublication", deletionTestWeb+"/group/project", false),
		"boolean":                      deletionReplaceFlag(base, "--acknowledge-catalog-unpublication", "true", false),
		"false":                        deletionReplaceFlag(base, "--acknowledge-catalog-unpublication", "false", false),
		"empty":                        deletionReplaceFlag(base, "--acknowledge-catalog-unpublication", "", false),
		"duplicate":                    append(append([]string(nil), base...), "--acknowledge-catalog-unpublication", deletionTestWeb+item.webPath),
		"broad-yes":                    append(append([]string(nil), withoutAck...), "--yes"),
		"missing-release-confirmation": deletionReplaceFlag(base, item.confirmation, "", true),
		"wrong-release-confirmation":   deletionReplaceFlag(base, item.confirmation, deletionTestWeb+"/group/project/-/releases/v2.0", false),
	} {
		t.Run(name, func(t *testing.T) {
			f := newDeletionFixture(t, item, "success")
			code, out := f.run(context.Background(), args, true)
			if code == 0 || out.OK || out.Error.Retryable || f.deletes != 0 || len(f.requests) != 0 || f.lookup.Load() != 0 || f.keyring.calls.Load() != 0 || f.catalogState != "published" || f.catalogVersions != 1 {
				t.Fatalf("exit=%d requests=%q writes=%d lookups=%d catalog=%s versions=%d", code, f.requests, f.deletes, f.lookup.Load(), f.catalogState, f.catalogVersions)
			}
			if name == "missing" || name == "sibling" {
				if out.Error.Code != "safety_violation" || !strings.Contains(out.Error.Message, "may unpublish the project's CI/CD Catalog resource") || !strings.Contains(out.Error.Message, "snapshot cannot guarantee absence of catalog effects") {
					t.Fatalf("preflight refusal hid the additional effect: %+v", out.Error)
				}
			}
		})
	}
}

func TestReleaseDeletionCatalogAcknowledgedOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name, mode, action, postcondition, tag, catalog, outcome string
		versions, deletes                                        int
		acknowledged                                             bool
	}{
		{"last-version", "success", "deleted", "not_found", "unchanged", "unpublished", "unverified", 1, 1, true},
		{"remaining-version", "success", "deleted", "not_found", "unchanged", "published", "unverified", 2, 1, true},
		{"drift", "drift", "not_applied", "not_checked", "unverified", "published", "not_attempted", 1, 0, false},
		{"lost-response", "lost-absence", "ambiguous", "not_found", "unchanged", "unpublished", "unverified", 1, 1, false},
		{"malformed-response", "malformed-delete", "ambiguous", "not_found", "unchanged", "unpublished", "unverified", 1, 1, false},
		{"wrong-response-identity", "wrong-delete-identity", "ambiguous", "not_found", "unchanged", "unpublished", "unverified", 1, 1, false},
		{"tag-drift", "tag-drift", "ambiguous", "unverified", "unverified", "unpublished", "unverified", 1, 1, true},
		{"forbidden", "delete-403", "rejected", "not_checked", "unverified", "published", "unverified", 1, 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			item := deletionCases()[2]
			f := newDeletionFixture(t, item, tc.mode)
			f.catalogVersions = tc.versions
			code, out := f.run(context.Background(), item.args(), true)
			wantSuccess := tc.action == "deleted"
			if (code == 0) != wantSuccess || out.OK != wantSuccess || out.Error.Retryable || f.deletes != tc.deletes || f.catalogState != tc.catalog {
				t.Fatalf("exit=%d writes=%d catalog=%s error=%+v", code, f.deletes, f.catalogState, out.Error)
			}
			wantVersions := tc.versions
			if tc.deletes == 1 && tc.action != "rejected" {
				wantVersions--
			}
			if f.catalogVersions != wantVersions {
				t.Fatalf("catalog versions=%d want=%d", f.catalogVersions, wantVersions)
			}
			r := out.Error.Receipt.Deletion
			if wantSuccess {
				r = out.Data.Deletion
			}
			if r.Action != tc.action || r.Postcondition != tc.postcondition || r.TagPostcondition != tc.tag || r.DeleteAttempted != (tc.deletes == 1) || r.Acknowledged != tc.acknowledged || r.URL != deletionTestWeb+item.webPath || r.Tag != item.selector || r.Expected["sha"] != deletionTestSHA {
				t.Fatalf("misleading release receipt: %+v", r)
			}
			assertReleaseCatalogUnpublicationReceipt(t, r, tc.outcome)
			if tc.action == "ambiguous" && (code != 6 || out.Error.Code != "ambiguous_delete") {
				t.Fatalf("lost uncertainty: exit=%d code=%s", code, out.Error.Code)
			}
			if len(f.requests) > 11 || f.selections.Load() != 1 || tc.tag == "unchanged" && f.tagReads != 2 {
				t.Fatalf("unexpected reconciliation: tags=%d requests=%q selections=%d", f.tagReads, f.requests, f.selections.Load())
			}
			requests, lookups := len(f.requests), f.lookup.Load()
			code, out = f.run(context.Background(), deletionReplaceFlag(item.args(), "--acknowledge-catalog-unpublication", "", true), true)
			if code == 0 || out.OK || out.Error.Code != "safety_violation" || len(f.requests) != requests || f.lookup.Load() != lookups || f.deletes != tc.deletes {
				t.Fatal("catalog acknowledgment was reused across invocations")
			}
		})
	}
}

func TestReleaseDeletionCatalogAcknowledgmentPreservesTagGuard(t *testing.T) {
	item := deletionCases()[2]
	f := newDeletionFixture(t, item, "pre-tag-drift")
	code, out := f.run(context.Background(), item.args(), true)
	if code == 0 || out.OK || out.Error.Code != "conflict" || f.deletes != 0 || f.tagReads != 1 || f.catalogState != "published" || f.catalogVersions != 1 {
		t.Fatalf("tag guard bypassed: exit=%d writes=%d catalog=%s error=%s", code, f.deletes, f.catalogState, out.Error.Code)
	}
}

func TestReleaseDeletionCatalogAcknowledgmentIsScoped(t *testing.T) {
	for _, item := range deletionCases() {
		if item.group == "release" {
			continue
		}
		t.Run(item.name, func(t *testing.T) {
			f := newDeletionFixture(t, item, "success")
			args := append(item.args(), "--acknowledge-catalog-unpublication", deletionTestWeb+item.webPath)
			code, out := f.run(context.Background(), args, true)
			if code == 0 || out.OK || len(f.requests) != 0 || f.lookup.Load() != 0 {
				t.Fatal("catalog acknowledgment authorized another resource")
			}
		})
	}
}

func TestReleaseDeletionHelpDisclosesCatalogUnpublication(t *testing.T) {
	var stdout bytes.Buffer
	deps := Dependencies{Runtime: runtimepkg.Dependencies{Stdout: &stdout, Stderr: &bytes.Buffer{}, LookupEnv: func(string) (string, bool) { t.Fatal("help consulted credentials"); return "", false }}, NewDelegate: func() delegateClient { t.Fatal("help consulted official glab"); return nil }}
	if code := Run(context.Background(), []string{"release", "delete", "--help"}, deps); code != 0 {
		t.Fatalf("help exit=%d", code)
	}
	for _, disclosure := range []string{"--acknowledge-catalog-unpublication URL", "--confirm-delete-release URL", "may unpublish the project's CI/CD Catalog resource", "last catalog version", "regardless of observed catalog state or version count", "snapshot cannot guarantee absence of catalog effects", "receipts do not verify catalog unpublication", "including after an ambiguous response", "The tag is never deleted"} {
		if !strings.Contains(stdout.String(), disclosure) {
			t.Fatalf("release deletion help omitted %q", disclosure)
		}
	}
}
