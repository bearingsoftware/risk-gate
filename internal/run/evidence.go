package run

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/bearingsoftware/risk-gate/internal/decision"
	"github.com/bearingsoftware/risk-gate/internal/score"
)

const (
	SkipNotConfigured   = "not_configured"
	SkipComputeFailed   = "compute_failed"
	SkipArtifactMissing = "artifact_missing"
	SkipArtifactTimeout = "artifact_timeout"
	SkipParseError      = "parse_error"
)

// ValidSkipReason reports whether r is one of the reasons the record may carry.
func ValidSkipReason(r string) bool {
	switch r {
	case SkipComputeFailed, SkipArtifactMissing, SkipArtifactTimeout, SkipParseError:
		return true
	}
	return false
}

type evidence struct {
	ok           bool
	skip         string
	base, head   float64
	apiBreak     bool
	generatedBy  string
	evidenceHash string
}

// loadEvidence reads the four files ctf evidence writes. Anything unreadable is a recorded skip, never an error.
func loadEvidence(o Options) evidence {
	switch {
	case o.EvidenceSource == "":
		return evidence{}
	case o.EvidenceError != "":
		return evidence{skip: o.EvidenceError}
	case o.EvidenceDir == "":
		return evidence{skip: SkipNotConfigured}
	}
	ev, err := readEvidence(o.EvidenceDir)
	if err != nil {
		o.Log.Printf("evidence skipped: %v", err)
		return evidence{skip: SkipParseError}
	}
	return ev
}

func readEvidence(dir string) (evidence, error) {
	base, err := readPercent(filepath.Join(dir, "coverage-base.txt"))
	if err != nil {
		return evidence{}, err
	}
	head, err := readPercent(filepath.Join(dir, "coverage-head.txt"))
	if err != nil {
		return evidence{}, err
	}
	for _, v := range []float64{base, head} {
		if math.IsNaN(v) || v < 0 || v > 100 {
			return evidence{}, fmt.Errorf("coverage %v out of range 0..100", v)
		}
	}
	ab, err := os.ReadFile(filepath.Join(dir, "api-break.txt"))
	if err != nil {
		return evidence{}, fmt.Errorf("reading api-break: %w", err)
	}
	apiBreak, err := strconv.ParseBool(strings.TrimSpace(string(ab)))
	if err != nil {
		return evidence{}, fmt.Errorf("api-break.txt: %w", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "evidence.json"))
	if err != nil {
		return evidence{}, fmt.Errorf("reading evidence.json: %w", err)
	}
	var doc struct {
		GeneratedBy string `json:"generatedBy"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return evidence{}, fmt.Errorf("evidence.json: %w", err)
	}
	sum := sha256.Sum256(raw)
	return evidence{ok: true, base: base, head: head, apiBreak: apiBreak, generatedBy: doc.GeneratedBy, evidenceHash: hex.EncodeToString(sum[:])}, nil
}

// apply feeds the evidence into the inputs the --coverage-* and --api-break flags set, unless a flag already did.
func (e evidence) apply(in *score.Input) {
	if in.CoverageDelta == nil {
		in.CoverageDelta = coverageDelta(e.base, e.head)
	}
	if in.APIBreak == nil {
		in.APIBreak = &e.apiBreak
	}
}

// record builds the record block, or nil when evidence is off. in is the input that was scored:
// the would_* fields come from a second pass over a copy with the evidence applied.
func (e evidence) record(o Options, in score.Input) *decision.Evidence {
	if o.EvidenceSource == "" {
		return nil
	}
	rec := &decision.Evidence{Source: o.EvidenceSource, Applied: e.ok && o.EvidenceApply, Status: "skipped"}
	if !e.ok {
		reason := e.skip
		rec.SkipReason = &reason
		return rec
	}
	rec.Status = "ok"
	rec.CoverageBase, rec.CoverageHead, rec.APIBreak = &e.base, &e.head, &e.apiBreak
	rec.GeneratedBy, rec.EvidenceSHA256 = &e.generatedBy, &e.evidenceHash
	would := in
	would.CoverageDelta = coverageDelta(e.base, e.head)
	would.APIBreak = &e.apiBreak
	res := score.Evaluate(would)
	rec.WouldScore, rec.WouldDetTier, rec.WouldFloors = &res.Score, &res.DetTier, res.Floors
	return rec
}
