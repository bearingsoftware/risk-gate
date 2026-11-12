// Package decision defines the decision/v1 record published by risk-gate.
package decision

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const (
	Schema   = "risk-gate.decision/v1"
	Marker   = "<!-- risk-gate:decision v1 -->"
	MaxFiles = 300
	MaxBytes = 60000
)

type Record struct {
	Schema          string      `json:"schema"`
	Repo            string      `json:"repo"`
	PR              int         `json:"pr"`
	BaseRef         string      `json:"base_ref"`
	BaseSHA         string      `json:"base_sha"`
	HeadRef         string      `json:"head_ref"`
	HeadSHA         string      `json:"head_sha"`
	Run             Run         `json:"run"`
	Mode            string      `json:"mode"`
	Scorer          Scorer      `json:"scorer"`
	TiersFileSHA256 string      `json:"tiers_file_sha256"`
	Lane            string      `json:"lane"`
	Author          Author      `json:"author"`
	PathTier        int         `json:"path_tier"`
	Floors          []Floor     `json:"floors"`
	Score           int         `json:"score"`
	ScoreItems      []ScoreItem `json:"score_items"`
	DetTier         int         `json:"det_tier"`
	Inputs          Inputs      `json:"inputs"`
	Unavailable     []string    `json:"unavailable"`
	Attestation     Attestation `json:"attestation"`
	Check           Check       `json:"check"`
	Evidence        *Evidence   `json:"evidence,omitempty"`
}

type Run struct {
	ID      int64     `json:"id"`
	Attempt int       `json:"attempt"`
	Event   string    `json:"event"`
	Action  string    `json:"action,omitempty"`
	At      time.Time `json:"at"`
}

type Scorer struct {
	Version       string `json:"version"`
	WeightsVer    string `json:"weights_version"`
	WeightsSHA256 string `json:"weights_sha256"`
}

type Author struct {
	Login string `json:"login"`
	Bot   bool   `json:"bot"`
}

type Floor struct {
	Rule   string `json:"rule"`
	Tier   int    `json:"tier"`
	Detail string `json:"detail,omitempty"`
}

type ScoreItem struct {
	Signal string `json:"signal"`
	Points int    `json:"points"`
	Value  any    `json:"value,omitempty"`
}

type Inputs struct {
	Title                string   `json:"title"`
	Labels               []string `json:"labels"`
	LinesAdded           int      `json:"lines_added"`
	LinesDeleted         int      `json:"lines_deleted"`
	FilesChanged         int      `json:"files_changed"`
	TopLevelDirs         int      `json:"top_level_dirs"`
	TestLines            int      `json:"test_lines"`
	NonTestLines         int      `json:"non_test_lines"`
	CoverageDelta        *float64 `json:"coverage_delta"`
	SastNewHigh          *int     `json:"sast_new_high"`
	APIBreak             *bool    `json:"api_break"`
	DepManifestChanged   bool     `json:"dep_manifest_changed"`
	AuthorCommits180d    *int     `json:"author_commits_180d"`
	FirstTimeContributor *bool    `json:"first_time_contributor"`
	HotFiles             *int     `json:"hot_files"`
	BotUpdateType        *string  `json:"bot_update_type"`
	Files                []File   `json:"files"`
	FilesTruncated       bool     `json:"files_truncated"`
}

type File struct {
	Path         string `json:"path"`
	PreviousPath string `json:"previous_path,omitempty"`
	Status       string `json:"status"`
	Additions    int    `json:"additions"`
	Deletions    int    `json:"deletions"`
	Tier         int    `json:"tier"`
}

type Attestation struct {
	Required bool       `json:"required"`
	Attested bool       `json:"attested"`
	Label    string     `json:"label"`
	By       *string    `json:"by"`
	At       *time.Time `json:"at"`
}

type Check struct {
	Conclusion string `json:"conclusion"`
	WouldBlock bool   `json:"would_block"`
	Reason     string `json:"reason,omitempty"`
}

// Evidence records component-test evidence. Unless Applied, it never changed the score, floors or tier;
// the would_* fields show what applying it would have produced.
type Evidence struct {
	Source         string   `json:"source"`
	Applied        bool     `json:"applied"`
	Status         string   `json:"status"`
	SkipReason     *string  `json:"skip_reason"`
	CoverageBase   *float64 `json:"coverage_base"`
	CoverageHead   *float64 `json:"coverage_head"`
	APIBreak       *bool    `json:"api_break"`
	GeneratedBy    *string  `json:"generated_by"`
	EvidenceSHA256 *string  `json:"evidence_sha256"`
	WouldScore     *int     `json:"would_score"`
	WouldDetTier   *int     `json:"would_det_tier"`
	WouldFloors    []Floor  `json:"would_floors"`
}

// Marshal encodes the record, capping inputs.files at MaxFiles and halving it
// until the result fits in MaxBytes.
func (r Record) Marshal() ([]byte, error) {
	if len(r.Inputs.Files) > MaxFiles {
		r.Inputs.Files = r.Inputs.Files[:MaxFiles]
		r.Inputs.FilesTruncated = true
	}
	for {
		b, err := json.Marshal(r)
		if err != nil {
			return nil, fmt.Errorf("encoding decision: %w", err)
		}
		if len(b) <= MaxBytes || len(r.Inputs.Files) == 0 {
			return b, nil
		}
		r.Inputs.Files = r.Inputs.Files[:len(r.Inputs.Files)/2]
		r.Inputs.FilesTruncated = true
	}
}

// CheckText is the check run output.text: the marker, then the record in a json fence.
// Backticks only occur inside JSON strings, so escaping them keeps a fence from appearing in the record.
func CheckText(record []byte) string {
	return Marker + "\n```json\n" + strings.ReplaceAll(string(record), "`", `\u0060`) + "\n```\n"
}
