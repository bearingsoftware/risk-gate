package run

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
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

const (
	maxTextFile     = 1 << 10
	maxEvidenceJSON = 1 << 20
)

var generatedByRe = regexp.MustCompile(`^ctf evidence [0-9A-Za-z.+-]{1,40}$`)

// readCapped reads a file of at most limit bytes; a longer one is an error, so PR-controlled input stays bounded.
func readCapped(p string, limit int64) ([]byte, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("%s is over %d bytes", filepath.Base(p), limit)
	}
	return b, nil
}

func readEvidencePercent(dir, name string) (float64, error) {
	b, err := readCapped(filepath.Join(dir, name), maxTextFile)
	if err != nil {
		return 0, err
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(string(b)), 64)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", name, err)
	}
	if math.IsNaN(v) || v < 0 || v > 100 {
		return 0, fmt.Errorf("%s: %v out of range 0..100", name, v)
	}
	return v, nil
}

func readEvidence(dir string) (evidence, error) {
	base, err := readEvidencePercent(dir, "coverage-base.txt")
	if err != nil {
		return evidence{}, err
	}
	head, err := readEvidencePercent(dir, "coverage-head.txt")
	if err != nil {
		return evidence{}, err
	}
	ab, err := readCapped(filepath.Join(dir, "api-break.txt"), maxTextFile)
	if err != nil {
		return evidence{}, err
	}
	apiBreak, err := strconv.ParseBool(strings.TrimSpace(string(ab)))
	if err != nil {
		return evidence{}, fmt.Errorf("api-break.txt: %w", err)
	}
	raw, err := readCapped(filepath.Join(dir, "evidence.json"), maxEvidenceJSON)
	if err != nil {
		return evidence{}, err
	}
	var doc struct {
		GeneratedBy string `json:"generatedBy"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return evidence{}, fmt.Errorf("evidence.json: %w", err)
	}
	if !generatedByRe.MatchString(doc.GeneratedBy) {
		return evidence{}, fmt.Errorf("evidence.json: generatedBy is not a ctf evidence version")
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
	e.apply(&would)
	res := score.Evaluate(would)
	rec.WouldScore, rec.WouldDetTier, rec.WouldFloors = &res.Score, &res.DetTier, res.Floors
	return rec
}
