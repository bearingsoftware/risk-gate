package decision

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	AISchema = "risk-gate.ai/v1"
	AIMarker = "<!-- risk-gate:ai v1 -->"
)

const (
	AIClassified = "classified"
	AISkipped    = "skipped"
)

const (
	SkipNoKey         = "no_key"
	SkipNotHumanLane  = "not_human_lane"
	SkipFork          = "fork"
	SkipRefusal       = "refusal"
	SkipTruncated     = "truncated"
	SkipParseError    = "parse_error"
	SkipCreditOrLimit = "credit_or_limit"
	SkipAPIError      = "api_error"
)

type AIRecord struct {
	Schema         string    `json:"schema"`
	Repo           string    `json:"repo"`
	PR             int       `json:"pr"`
	BaseSHA        string    `json:"base_sha"`
	HeadSHA        string    `json:"head_sha"`
	Run            Run       `json:"run"`
	Status         string    `json:"status"`
	SkipReason     *string   `json:"skip_reason"`
	PromptVersion  string    `json:"prompt_version"`
	PromptSHA256   string    `json:"prompt_sha256"`
	ModelRequested string    `json:"model_requested"`
	ModelUsed      *string   `json:"model_used"`
	DetTier        int       `json:"det_tier"`
	WeightsVersion string    `json:"weights_version"`
	Lane           string    `json:"lane"`
	AI             *AIResult `json:"ai"`
	Usage          *AIUsage  `json:"usage"`
	CostUSD        *float64  `json:"cost_usd"`
	LatencyMS      *int64    `json:"latency_ms"`
	DiffTruncated  bool      `json:"diff_truncated"`
}

type AIResult struct {
	Tier             int      `json:"tier"`
	Confidence       float64  `json:"confidence"`
	Reasons          []string `json:"reasons"`
	SecurityConcerns []string `json:"security_concerns"`
}

type AIUsage struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
}

func (r AIRecord) Marshal() ([]byte, error) {
	b, err := json.Marshal(r)
	if err != nil {
		return nil, fmt.Errorf("encoding ai record: %w", err)
	}
	return b, nil
}

// AICheckText is the check run output.text: the marker, then the record in a json fence.
func AICheckText(record []byte) string {
	return AIMarker + "\n```json\n" + strings.ReplaceAll(string(record), "`", `\u0060`) + "\n```\n"
}

// ParseAICheckText reads the record back from output.text; ok is false when the marker or fence is missing or the JSON is bad.
func ParseAICheckText(text string) (AIRecord, bool) {
	rest, ok := strings.CutPrefix(text, AIMarker+"\n```json\n")
	if !ok {
		return AIRecord{}, false
	}
	body, _, ok := strings.Cut(rest, "\n```")
	if !ok {
		return AIRecord{}, false
	}
	var r AIRecord
	if err := json.Unmarshal([]byte(body), &r); err != nil || r.Schema != AISchema {
		return AIRecord{}, false
	}
	return r, true
}
