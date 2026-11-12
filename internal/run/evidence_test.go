package run_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/bearingsoftware/risk-gate/internal/run"
)

func writeEvidence(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func goodEvidence() map[string]string {
	return map[string]string{"coverage-base.txt": "80.00", "coverage-head.txt": "78.50", "api-break.txt": "false", "evidence.json": `{"version":1,"generatedBy":"ctf evidence 0.3.16"}`}
}

func without(m map[string]string, k string) map[string]string {
	out := map[string]string{}
	for key, v := range m {
		if key != k {
			out[key] = v
		}
	}
	return out
}

func with(m map[string]string, k, v string) map[string]string {
	out := without(m, "")
	out[k] = v
	return out
}

func TestEvidenceSkipReasons(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(o *run.Options)
		files  map[string]string
		want   string
	}{
		{"compute_failed", func(o *run.Options) { o.EvidenceError = run.SkipComputeFailed }, nil, "compute_failed"},
		{"artifact_missing", func(o *run.Options) { o.EvidenceError = run.SkipArtifactMissing }, nil, "artifact_missing"},
		{"artifact_timeout", func(o *run.Options) { o.EvidenceError = run.SkipArtifactTimeout }, nil, "artifact_timeout"},
		{"not_configured", func(o *run.Options) {}, nil, "not_configured"},
		{"error wins over a dir", func(o *run.Options) { o.EvidenceError = run.SkipComputeFailed }, goodEvidence(), "compute_failed"},
		{"missing dir", func(o *run.Options) { o.EvidenceDir = "/nonexistent/evidence" }, nil, "parse_error"},
		{"missing coverage file", nil, without(goodEvidence(), "coverage-head.txt"), "parse_error"},
		{"coverage not a number", nil, with(goodEvidence(), "coverage-base.txt", "high"), "parse_error"},
		{"coverage NaN", nil, with(goodEvidence(), "coverage-base.txt", "NaN"), "parse_error"},
		{"coverage over 100", nil, with(goodEvidence(), "coverage-head.txt", "140"), "parse_error"},
		{"coverage negative", nil, with(goodEvidence(), "coverage-head.txt", "-3"), "parse_error"},
		{"api-break not a bool", nil, with(goodEvidence(), "api-break.txt", "maybe"), "parse_error"},
		{"missing evidence.json", nil, without(goodEvidence(), "evidence.json"), "parse_error"},
		{"evidence.json not json", nil, with(goodEvidence(), "evidence.json", "{"), "parse_error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			o := opts(t, "shadow", "opened")
			o.EvidenceSource = "compute"
			o.EvidenceApply = true
			if tt.files != nil {
				o.EvidenceDir = writeEvidence(t, tt.files)
			}
			if tt.mutate != nil {
				tt.mutate(&o)
			}
			plain, err := run.Run(context.Background(), opts(t, "shadow", "opened"), fake(nil, "src/a.go"))
			if err != nil {
				t.Fatal(err)
			}
			rec, err := run.Run(context.Background(), o, fake(nil, "src/a.go"))
			if err != nil {
				t.Fatalf("skip must not be fatal: %v", err)
			}
			e := rec.Evidence
			if e == nil || e.Status != "skipped" || e.SkipReason == nil || *e.SkipReason != tt.want || e.Applied {
				t.Fatalf("evidence = %+v", e)
			}
			if e.CoverageBase != nil || e.CoverageHead != nil || e.APIBreak != nil || e.WouldScore != nil || e.WouldFloors != nil {
				t.Fatalf("a skip carries values: %+v", e)
			}
			if rec.Inputs.CoverageDelta != nil || rec.Inputs.APIBreak != nil {
				t.Fatal("a skipped evidence set an input; it must stay unavailable, never 0")
			}
			if rec.Score != plain.Score || rec.DetTier != plain.DetTier || len(rec.Unavailable) != len(plain.Unavailable) {
				t.Fatalf("a skip changed the decision: %+v", rec)
			}
		})
	}
}

func TestEvidenceApplyFeedsInputs(t *testing.T) {
	t.Parallel()
	dir := writeEvidence(t, with(goodEvidence(), "api-break.txt", "true\n"))

	o := opts(t, "shadow", "opened")
	o.EvidenceSource, o.EvidenceDir = "compute", dir
	rec, err := run.Run(context.Background(), o, fake(nil, "src/a.go"))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Inputs.CoverageDelta != nil || rec.Inputs.APIBreak != nil || rec.DetTier != 1 {
		t.Fatalf("recorded-only evidence reached the inputs: %+v tier %d", rec.Inputs, rec.DetTier)
	}
	var ua []string
	for _, u := range rec.Unavailable {
		if u == "coverage_delta" || u == "api_break" {
			ua = append(ua, u)
		}
	}
	if len(ua) != 2 {
		t.Fatalf("unavailable = %v", rec.Unavailable)
	}
	if *rec.Evidence.WouldDetTier != 2 || *rec.Evidence.CoverageHead != 78.5 || !*rec.Evidence.APIBreak {
		t.Fatalf("evidence = %+v", rec.Evidence)
	}

	o.EvidenceApply = true
	rec, err = run.Run(context.Background(), o, fake(nil, "src/a.go"))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Inputs.CoverageDelta == nil || *rec.Inputs.CoverageDelta != -1.5 || rec.Inputs.APIBreak == nil || !*rec.Inputs.APIBreak || rec.DetTier != 2 || !rec.Evidence.Applied {
		t.Fatalf("applied evidence: %+v tier %d", rec.Inputs, rec.DetTier)
	}
}
