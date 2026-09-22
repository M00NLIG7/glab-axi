package product

import (
	"slices"
	"strconv"
	"strings"

	"gl-axi/internal/contract/uxv1"
	"gl-axi/internal/limits"
)

// Creation selection never replaces an existing MR's collections. Numeric
// identities avoid username/name lookup ambiguity and implicit label creation.
type mrCreationMetadata struct {
	AssigneeIDs []int64 `json:"assignee_ids,omitempty"`
	ReviewerIDs []int64 `json:"reviewer_ids,omitempty"`
	MilestoneID int64   `json:"milestone_id,omitempty"`
	Draft       bool    `json:"draft,omitempty"`
}

type mrMetadataIdentity struct {
	ID int64 `json:"id"`
}

func parseMRCreationMetadata(parsed Parsed) (*mrCreationMetadata, error) {
	selection := &mrCreationMetadata{Draft: parsed.Booleans["--draft"]}
	for _, field := range []struct {
		flag string
		dest *[]int64
	}{{"--assignee-id", &selection.AssigneeIDs}, {"--reviewer-id", &selection.ReviewerIDs}} {
		flag, dest := field.flag, field.dest
		values := parsed.MultiValues[flag]
		if len(values) > 20 {
			return nil, uxv1.NewError(uxv1.CodeValidation, flag+" accepts at most 20 identities")
		}
		for _, raw := range values {
			id, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || id < 1 || strconv.FormatInt(id, 10) != raw || slices.Contains(*dest, id) {
				return nil, uxv1.NewError(uxv1.CodeValidation, flag+" requires unique canonical positive numeric IDs")
			}
			*dest = append(*dest, id)
		}
		slices.Sort(*dest)
	}
	if raw := parsed.Values["--milestone-id"]; raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id < 1 || strconv.FormatInt(id, 10) != raw {
			return nil, uxv1.NewError(uxv1.CodeValidation, "--milestone-id requires a canonical positive numeric ID")
		}
		selection.MilestoneID = id
	}
	if len(selection.AssigneeIDs) == 0 && len(selection.ReviewerIDs) == 0 && selection.MilestoneID == 0 && !selection.Draft {
		return nil, nil
	}
	return selection, nil
}

func (selection *mrCreationMetadata) title(title string) (string, error) {
	if selection != nil && selection.Draft && !strings.HasPrefix(strings.ToLower(title), "draft:") {
		title = "Draft: " + title
	}
	if len(title) > limits.MaxTitleBytes {
		return "", uxv1.NewError(uxv1.CodeValidation, "draft prefix exceeds the title byte limit")
	}
	return title, nil
}

func (selection *mrCreationMetadata) addInput(input map[string]any) {
	if selection == nil {
		return
	}
	if len(selection.AssigneeIDs) > 0 {
		input["assignee_ids"] = selection.AssigneeIDs
	}
	if len(selection.ReviewerIDs) > 0 {
		input["reviewer_ids"] = selection.ReviewerIDs
	}
	if selection.MilestoneID > 0 {
		input["milestone_id"] = selection.MilestoneID
	}
}

func (selection *mrCreationMetadata) matches(record upstreamMR) bool {
	if selection == nil {
		return true
	}
	matchIDs := func(expected []int64, actual []mrMetadataIdentity) bool {
		if len(expected) == 0 {
			return true
		}
		if len(actual) != len(expected) {
			return false
		}
		ids := make([]int64, len(actual))
		for index, value := range actual {
			ids[index] = value.ID
		}
		slices.Sort(ids)
		return slices.Equal(expected, ids)
	}
	return (!selection.Draft || record.Draft) && matchIDs(selection.AssigneeIDs, record.Assignees) && matchIDs(selection.ReviewerIDs, record.Reviewers) && (selection.MilestoneID == 0 || record.Milestone != nil && record.Milestone.ID == selection.MilestoneID)
}
