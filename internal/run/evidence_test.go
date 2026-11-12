package run_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bearingsoftware/risk-gate/internal/decision"
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

func TestEvidenceHostileInputsAreParseErrors(t *testing.T) {
	t.Parallel()
	tests := map[string]map[string]string{
		"oversized generatedBy":   with(goodEvidence(), "evidence.json", `{"generatedBy":"ctf evidence `+strings.Repeat("1", 200<<10)+`"}`),
		"generatedBy with fence":  with(goodEvidence(), "evidence.json", "{\"generatedBy\":\"ctf evidence 1`\\n```\"}"),
		"generatedBy not ctf":     with(goodEvidence(), "evidence.json", `{"generatedBy":"something else"}`),
		"missing generatedBy":     with(goodEvidence(), "evidence.json", `{}`),
		"oversized evidence.json": with(goodEvidence(), "evidence.json", `{"generatedBy":"ctf evidence 1","pad":"`+strings.Repeat("x", 2<<20)+`"}`),
		"oversized coverage":      with(goodEvidence(), "coverage-base.txt", "80"+strings.Repeat(" ", 2<<10)),
		"oversized api-break":     with(goodEvidence(), "api-break.txt", "true"+strings.Repeat("\n", 2<<10)),
	}
	for name, files := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			o := opts(t, "shadow", "opened")
			o.EvidenceSource, o.EvidenceDir = "compute", writeEvidence(t, files)
			rec, err := run.Run(context.Background(), o, fake(nil, "src/a.go"))
			if err != nil {
				t.Fatal(err)
			}
			e := rec.Evidence
			if e == nil || e.Status != "skipped" || e.SkipReason == nil || *e.SkipReason != run.SkipParseError {
				t.Fatalf("evidence = %+v", e)
			}
			b, err := rec.Marshal()
			if err != nil || len(b) > decision.MaxBytes {
				t.Fatalf("record %d bytes, err %v", len(b), err)
			}
		})
	}
}

func TestAcceptedEvidenceKeepsRecordUnderCap(t *testing.T) {
	t.Parallel()
	by := "ctf evidence " + strings.Repeat("9", 40)
	files := with(goodEvidence(), "evidence.json", `{"generatedBy":"`+by+`","pad":"`+strings.Repeat("x", 1<<19)+`"}`)
	f := fake(nil)
	for i := range 3000 {
		f.FileList = append(f.FileList, fake(nil, strings.Repeat("long-directory-name/", 12)+string(rune('a'+i%26))+strings.Repeat("f", 8)+"_"+strings.Repeat("0", 4)+".go").FileList...)
	}
	o := opts(t, "shadow", "opened")
	o.EvidenceSource, o.EvidenceDir, o.EvidenceApply = "compute", writeEvidence(t, files), true
	rec, err := run.Run(context.Background(), o, f)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Evidence == nil || rec.Evidence.Status != "ok" || *rec.Evidence.GeneratedBy != by {
		t.Fatalf("evidence = %+v", rec.Evidence)
	}
	if b, err := rec.Marshal(); err != nil || len(b) > decision.MaxBytes {
		t.Fatalf("record %d bytes, err %v", len(b), err)
	}
}

func TestManualFlagsWinOverEvidence(t *testing.T) {
	t.Parallel()
	dir := writeEvidence(t, with(goodEvidence(), "api-break.txt", "true"))
	manual := func(t *testing.T, o *run.Options) {
		p := filepath.Join(t.TempDir(), "cov")
		if err := os.WriteFile(p, []byte("50"), 0o644); err != nil {
			t.Fatal(err)
		}
		o.CoverageBase, o.CoverageHead = p, p
		no := false
		o.APIBreak = &no
	}
	var recs [2]decision.Record
	for i, apply := range []bool{false, true} {
		o := opts(t, "shadow", "opened")
		o.EvidenceSource, o.EvidenceDir, o.EvidenceApply = "compute", dir, apply
		manual(t, &o)
		rec, err := run.Run(context.Background(), o, fake(nil, "src/a.go"))
		if err != nil {
			t.Fatal(err)
		}
		recs[i] = rec
		if *rec.Inputs.CoverageDelta != 0 || *rec.Inputs.APIBreak || rec.DetTier != 1 {
			t.Fatalf("apply=%v: manual flags lost: %+v tier %d", apply, rec.Inputs, rec.DetTier)
		}
		if *rec.Evidence.WouldDetTier != rec.DetTier || *rec.Evidence.WouldScore != rec.Score {
			t.Fatalf("apply=%v: would_* ignores the manual flags: %+v", apply, rec.Evidence)
		}
	}
}
