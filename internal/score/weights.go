package score

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

type Weights struct {
	Version        string      `yaml:"version"`
	Size           []SizeBand  `yaml:"size"`
	DiffusionFiles Over        `yaml:"diffusion_files"`
	DiffusionDirs  Over        `yaml:"diffusion_dirs"`
	NoTests        NoTests     `yaml:"no_tests"`
	BugFix         Points      `yaml:"bug_fix"`
	RevertHotfix   Points      `yaml:"revert_or_hotfix"`
	CoverageDrop   Points      `yaml:"coverage_drop"`
	AuthorTenure   Tenure      `yaml:"author_tenure"`
	HotFiles       HotFiles    `yaml:"hot_files"`
	HotLookback    HotLookback `yaml:"hot_file_lookback"`
	Mapping        TierMapping `yaml:"mapping"`
	SHA256         string      `yaml:"-"`
}

type SizeBand struct {
	MaxLines *int `yaml:"max_lines"`
	Points   int  `yaml:"points"`
}

type Points struct {
	Points int `yaml:"points"`
}

type Over struct {
	Over   int `yaml:"over"`
	Points int `yaml:"points"`
}

type NoTests struct {
	NonTestLinesOver int `yaml:"non_test_lines_over"`
	Points           int `yaml:"points"`
}

type Tenure struct {
	Commits180dUnder int `yaml:"commits_180d_under"`
	Points           int `yaml:"points"`
}

type HotFiles struct {
	AtLeast int `yaml:"at_least"`
	Points  int `yaml:"points"`
}

type HotLookback struct {
	Days              int `yaml:"days"`
	MaxPRs            int `yaml:"max_prs"`
	TouchedByMoreThan int `yaml:"touched_by_more_than"`
}

type TierMapping struct {
	HumanTier0MaxScore int `yaml:"human_tier0_max_score"`
	Tier1MaxScore      int `yaml:"tier1_max_score"`
}

func ParseWeights(data []byte) (*Weights, error) {
	var w Weights
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(&w); err != nil {
		return nil, fmt.Errorf("parsing weights: %w", err)
	}
	if w.Version == "" || len(w.Size) == 0 {
		return nil, fmt.Errorf("weights: version and size are required")
	}
	sum := sha256.Sum256(data)
	w.SHA256 = hex.EncodeToString(sum[:])
	return &w, nil
}

func (w *Weights) sizePoints(lines int) int {
	for _, b := range w.Size {
		if b.MaxLines == nil || lines <= *b.MaxLines {
			return b.Points
		}
	}
	return 0
}
