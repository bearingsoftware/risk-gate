package score_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"os"
	"reflect"
	"testing"

	"github.com/bearingsoftware/risk-gate/internal/decision"
	"github.com/bearingsoftware/risk-gate/internal/run"
)

const evidenceJSON = `{"version":1,"base":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","head":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","generatedBy":"ctf evidence 0.3.16"}`

// smallScenario is the tier1-small-human scenario, which has no floors and a score of 0.
func smallScenario(t *testing.T) scenario {
	t.Helper()
	for _, sc := range scenarios() {
		if sc.fixture == "tier1-small-human.json" {
			return sc
		}
	}
	t.Fatal("scenario missing")
	return scenario{}
}

func evidenceDir(t *testing.T, base, head, apiBreak string) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, "coverage-base.txt", base)
	writeFile(t, dir, "coverage-head.txt", head)
	writeFile(t, dir, "api-break.txt", apiBreak)
	writeFile(t, dir, "evidence.json", evidenceJSON)
	return dir
}

func runScenario(t *testing.T, sc scenario, mutate func(*run.Options)) decision.Record {
	t.Helper()
	sc.opts.PR = sc.fake.PR.Number
	sc.opts.TiersPath = writeFile(t, t.TempDir(), "tiers.yml", sc.tiers)
	sc.opts.WeightsPath = "../../weights/v1.yml"
	sc.opts.Log = log.New(os.Stderr, "", 0)
	if mutate != nil {
		mutate(&sc.opts)
	}
	rec, err := run.Run(context.Background(), sc.opts, sc.fake)
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

type evidenceCase struct {
	fixture string
	mutate  func(t *testing.T) func(*run.Options)
}

func evidenceCases() []evidenceCase {
	return []evidenceCase{
		{"evidence-applied.json", func(t *testing.T) func(*run.Options) {
			dir := evidenceDir(t, "82.35", "70.10", "true")
			return func(o *run.Options) { o.EvidenceSource, o.EvidenceDir, o.EvidenceApply = "compute", dir, true }
		}},
		{"evidence-recorded-only.json", func(t *testing.T) func(*run.Options) {
			dir := evidenceDir(t, "82.35", "70.10", "true")
			return func(o *run.Options) { o.EvidenceSource, o.EvidenceDir = "artifact", dir }
		}},
		{"evidence-skipped.json", func(t *testing.T) func(*run.Options) {
			return func(o *run.Options) { o.EvidenceSource, o.EvidenceError = "artifact", run.SkipArtifactTimeout }
		}},
	}
}

func TestGoldenEvidence(t *testing.T) {
	for _, c := range evidenceCases() {
		t.Run(c.fixture, func(t *testing.T) {
			rec := runScenario(t, smallScenario(t), c.mutate(t))
			got, err := json.Marshal(rec)
			if err != nil {
				t.Fatal(err)
			}
			validateAgainstSchema(t, got)
			want, err := os.ReadFile("../../testdata/decisions/" + c.fixture)
			if err != nil {
				t.Fatal(err)
			}
			g, w := asMap(t, got), asMap(t, want)
			for _, m := range []map[string]any{g, w} {
				del(m, "inputs", "files")
				del(m, "scorer", "weights_sha256")
				del(m, "tiers_file_sha256")
				del(m, "check", "reason")
			}
			if !reflect.DeepEqual(g, w) {
				gj, _ := json.MarshalIndent(g, "", " ")
				wj, _ := json.MarshalIndent(w, "", " ")
				t.Fatalf("decision differs from fixture\n--- got\n%s\n--- want\n%s", gj, wj)
			}
		})
	}
}

// Without evidence flags the record is the exact bytes the scorer produced before the evidence block existed.
func TestNoEvidenceIsByteIdentical(t *testing.T) {
	for _, sc := range scenarios() {
		t.Run(sc.fixture, func(t *testing.T) {
			rec := runScenario(t, sc, func(o *run.Options) {
				if sc.sarif {
					empty := writeFile(t, t.TempDir(), "empty.sarif", `{"runs":[{"tool":{"driver":{"name":"x"}},"results":[]}]}`)
					o.SarifBase, o.SarifHead = empty, empty
				}
			})
			got, err := rec.Marshal()
			if err != nil {
				t.Fatal(err)
			}
			want, err := os.ReadFile("../../testdata/noevidence/" + sc.fixture)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("record differs from the pre-evidence bytes\n--- got\n%s\n--- want\n%s", got, want)
			}
			if bytes.Contains(got, []byte(`"evidence"`)) {
				t.Fatal("evidence key present")
			}
		})
	}
}

// would_* must equal what applying the same evidence produces, and recording must not move the decision.
func TestWouldEqualsApplied(t *testing.T) {
	for _, sc := range scenarios() {
		t.Run(sc.fixture, func(t *testing.T) {
			dir := evidenceDir(t, "82.35", "60.00", "true")
			plain := runScenario(t, sc, nil)
			shadow := runScenario(t, sc, func(o *run.Options) { o.EvidenceSource, o.EvidenceDir = "compute", dir })
			applied := runScenario(t, sc, func(o *run.Options) { o.EvidenceSource, o.EvidenceDir, o.EvidenceApply = "compute", dir, true })

			if shadow.Score != plain.Score || shadow.DetTier != plain.DetTier || !reflect.DeepEqual(shadow.Floors, plain.Floors) ||
				!reflect.DeepEqual(shadow.Inputs, plain.Inputs) || !reflect.DeepEqual(shadow.Unavailable, plain.Unavailable) {
				t.Fatal("recording evidence changed the decision")
			}
			e, a := shadow.Evidence, applied.Evidence
			if e == nil || a == nil || e.Applied || !a.Applied {
				t.Fatalf("applied flags: %+v %+v", e, a)
			}
			if *e.WouldScore != applied.Score || *e.WouldDetTier != applied.DetTier || !reflect.DeepEqual(e.WouldFloors, applied.Floors) {
				t.Fatalf("would %d/%d/%v, applied %d/%d/%v", *e.WouldScore, *e.WouldDetTier, e.WouldFloors, applied.Score, applied.DetTier, applied.Floors)
			}
			if !reflect.DeepEqual(e.WouldFloors, a.WouldFloors) || *a.WouldScore != applied.Score {
				t.Fatal("applied record's would_* differs from its own decision")
			}
		})
	}
}
