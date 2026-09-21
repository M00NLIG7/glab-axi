package product

const planningOrderingEffect = "GitLab may initialize missing relative positions for fetched issues and shift existing sibling positions, including beyond displayed items; changes are not measured or rolled back."

// PlanningOrderingReceipt describes permission and uncertainty, never an
// invented changed-record count. An attempted delegate call may fail after
// GitLab changed positions; even a successful response cannot attest changes.
type PlanningOrderingReceipt struct {
	Acknowledged      bool   `json:"acknowledged"`
	ProviderSchema    string `json:"provider_schema_baseline"`
	Tiers             string `json:"provider_tiers"`
	Host              string `json:"host"`
	ScopeKind         string `json:"scope_kind"`
	ScopePath         string `json:"scope_path"`
	BoardID           int64  `json:"board_id"`
	ListID            int64  `json:"list_id"`
	Effect            string `json:"possible_effect"`
	Outcome           string `json:"outcome"`
	RequestsAttempted int    `json:"delegate_attempts"`
}
