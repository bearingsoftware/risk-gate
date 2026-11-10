package run_test

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bearingsoftware/risk-gate/internal/decision"
	"github.com/bearingsoftware/risk-gate/internal/gh"
	"github.com/bearingsoftware/risk-gate/internal/ghfake"
	"github.com/bearingsoftware/risk-gate/internal/run"
)

const tiersDoc = `version: 1
default_tier: 1
tiers:
  2:
    paths: ["auth/**"]
attestation:
  label: attested
  permitted: ["boss"]
`

func opts(t *testing.T, mode, action string) run.Options {
	t.Helper()
	p := filepath.Join(t.TempDir(), "tiers.yml")
	if err := os.WriteFile(p, []byte(tiersDoc), 0o644); err != nil {
		t.Fatal(err)
	}
	return run.Options{
		Repo: "o/r", PR: 3, TiersPath: p, WeightsPath: "../../weights/v1.yml", Mode: mode,
		RunID: 9, RunAttempt: 2, Event: "pull_request_target", EventAction: action,
		Now: func() time.Time { return time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC) },
		Log: log.New(io.Discard, "", 0),
	}
}

func fake(labels []string, paths ...string) *ghfake.Fake {
	f := &ghfake.Fake{
		PR:           gh.PullRequest{Number: 3, Title: "feat: x", Author: "alice", BaseRef: "main", BaseSHA: strings.Repeat("a", 40), HeadRef: "feat/x", HeadSHA: strings.Repeat("b", 40), Labels: labels},
		CommitsSince: map[bool]int{true: 40, false: 40},
	}
	for _, p := range paths {
		f.FileList = append(f.FileList, gh.File{Path: p, Status: "modified", Additions: 1})
	}
	return f
}

func TestAttestation(t *testing.T) {
	t.Parallel()
	label := gh.LabelEvent{Actor: "boss", At: time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)}
	stranger := gh.LabelEvent{Actor: "alice", At: label.At}
	tests := []struct {
		name        string
		mode        string
		action      string
		labels      []string
		ev          *gh.LabelEvent
		files       []string
		wantRemoved bool
		required    bool
		attested    bool
		conclusion  string
		wouldBlock  bool
	}{
		{"tier2 unattested", "enforce", "opened", nil, nil, []string{"auth/a.go"}, false, true, false, "failure", true},
		{"tier2 attested by permitted", "enforce", "labeled", []string{"attested"}, &label, []string{"auth/a.go"}, false, true, true, "success", false},
		{"tier2 labelled by non-permitted", "enforce", "labeled", []string{"attested"}, &stranger, []string{"auth/a.go"}, false, true, false, "failure", true},
		{"push removes label", "enforce", "synchronize", []string{"attested"}, &label, []string{"auth/a.go"}, true, true, false, "failure", true},
		{"push without label", "enforce", "synchronize", nil, nil, []string{"auth/a.go"}, false, true, false, "failure", true},
		{"push removes label on tier 1 too", "enforce", "synchronize", []string{"attested"}, &label, []string{"src/a.go"}, true, false, false, "success", false},
		{"tier1 needs nothing", "enforce", "opened", nil, nil, []string{"src/a.go"}, false, false, false, "success", false},
		{"shadow tier2 never fails", "shadow", "opened", nil, nil, []string{"auth/a.go"}, false, true, false, "success", true},
		{"shadow tier1", "shadow", "opened", nil, nil, []string{"src/a.go"}, false, false, false, "success", false},
		{"shadow leaves labels alone", "shadow", "synchronize", []string{"attested"}, &label, []string{"auth/a.go"}, false, true, false, "success", true},
		{"shadow attested", "shadow", "labeled", []string{"attested"}, &label, []string{"auth/a.go"}, false, true, true, "success", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := fake(tc.labels, tc.files...)
			f.LabelEv = tc.ev
			rec, err := run.Run(context.Background(), opts(t, tc.mode, tc.action), f)
			if err != nil {
				t.Fatal(err)
			}
			if got := len(f.Removed) == 1; got != tc.wantRemoved {
				t.Errorf("removed = %v", f.Removed)
			}
			if tc.wantRemoved && f.Removed[0] != "attested" {
				t.Errorf("removed %v", f.Removed)
			}
			a := rec.Attestation
			if a.Required != tc.required || a.Attested != tc.attested || a.Label != "attested" {
				t.Errorf("attestation = %+v", a)
			}
			if rec.Check.Conclusion != tc.conclusion || rec.Check.WouldBlock != tc.wouldBlock {
				t.Errorf("check = %+v", rec.Check)
			}
		})
	}
}

func TestAttestedRecordsActor(t *testing.T) {
	t.Parallel()
	f := fake([]string{"attested"}, "auth/a.go")
	f.LabelEv = &gh.LabelEvent{Actor: "boss", At: time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)}
	rec, err := run.Run(context.Background(), opts(t, "enforce", "labeled"), f)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Attestation.By == nil || *rec.Attestation.By != "boss" || rec.Attestation.At == nil || rec.Attestation.At.Hour() != 9 {
		t.Fatalf("attestation = %+v", rec.Attestation)
	}
	if rec.Run.Event != "pull_request" || rec.Run.ID != 9 || rec.Run.Attempt != 2 || rec.Run.Action != "labeled" {
		t.Fatalf("run = %+v", rec.Run)
	}
}

func TestEnforceNeedsAttestationBlock(t *testing.T) {
	t.Parallel()
	o := opts(t, "enforce", "opened")
	if err := os.WriteFile(o.TiersPath, []byte("version: 1\ndefault_tier: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := run.Run(context.Background(), o, fake(nil, "a.go")); err == nil {
		t.Fatal("expected an error")
	}
}

func TestBadInputs(t *testing.T) {
	t.Parallel()
	o := opts(t, "bogus", "")
	if _, err := run.Run(context.Background(), o, fake(nil, "a.go")); err == nil {
		t.Error("bad mode accepted")
	}
	o = opts(t, "shadow", "")
	o.Event = "push"
	if _, err := run.Run(context.Background(), o, fake(nil, "a.go")); err == nil {
		t.Error("unsupported event accepted")
	}
}

func TestPublishCheckRun(t *testing.T) {
	t.Parallel()
	f := fake(nil, "auth/a.go")
	o := opts(t, "enforce", "opened")
	o.Publish = true
	rec, err := run.Run(context.Background(), o, f)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Checks) != 1 {
		t.Fatalf("checks = %d", len(f.Checks))
	}
	c := f.Checks[0]
	if c.Name != "risk-gate" || c.HeadSHA != rec.HeadSHA || c.Conclusion != "failure" {
		t.Fatalf("check = %+v", c)
	}
	if !strings.HasPrefix(c.Text, "<!-- risk-gate:decision v1 -->\n```json\n") || !strings.HasSuffix(c.Text, "\n```\n") {
		t.Fatalf("text = %q", c.Text)
	}
	body := strings.TrimSuffix(strings.TrimPrefix(c.Text, "<!-- risk-gate:decision v1 -->\n```json\n"), "\n```\n")
	var back decision.Record
	if err := json.Unmarshal([]byte(body), &back); err != nil || back.PR != 3 || back.DetTier != 2 {
		t.Fatalf("embedded json: %v %+v", err, back)
	}
	if !strings.Contains(c.Summary, "| Tier | 2 |") {
		t.Fatalf("summary = %q", c.Summary)
	}

	f2 := fake(nil, "a.go")
	if _, err := run.Run(context.Background(), opts(t, "shadow", "opened"), f2); err != nil || len(f2.Checks) != 0 {
		t.Fatalf("published without --publish: %v %d", err, len(f2.Checks))
	}
}

func TestFirstTimeContributor(t *testing.T) {
	t.Parallel()
	f := fake(nil, "src/a.go")
	f.CommitsSince = map[bool]int{true: 0, false: 0}
	rec, err := run.Run(context.Background(), opts(t, "shadow", "opened"), f)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Inputs.FirstTimeContributor == nil || !*rec.Inputs.FirstTimeContributor || rec.DetTier != 2 {
		t.Fatalf("first-time = %v det = %d", rec.Inputs.FirstTimeContributor, rec.DetTier)
	}
	f.CommitsSince = map[bool]int{true: 0, false: 8}
	rec, _ = run.Run(context.Background(), opts(t, "shadow", "opened"), f)
	if *rec.Inputs.FirstTimeContributor || *rec.Inputs.AuthorCommits180d != 0 || rec.DetTier != 1 {
		t.Fatalf("lapsed contributor: %v %v det %d", *rec.Inputs.FirstTimeContributor, *rec.Inputs.AuthorCommits180d, rec.DetTier)
	}
}

func TestHotFilesNeedMoreThanThreePRs(t *testing.T) {
	t.Parallel()
	f := fake(nil, "src/a.go", "src/b.go", "src/c.go")
	f.Merged = gh.MergedPRs{Examined: 4, Files: [][]string{
		{"src/a.go", "src/b.go", "src/b.go"},
		{"src/a.go", "src/b.go"},
		{"src/a.go", "src/b.go", "src/c.go"},
		{"src/a.go", "src/c.go"},
	}}
	rec, err := run.Run(context.Background(), opts(t, "shadow", "opened"), f)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Inputs.HotFiles == nil || *rec.Inputs.HotFiles != 1 {
		t.Fatalf("hot files = %v", rec.Inputs.HotFiles)
	}
}

func TestOptionalSignals(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	base := write("base.sarif", `{"runs":[{"tool":{"driver":{"rules":[]}},"results":[
	  {"ruleId":"r1","level":"error","message":{"text":"m"},"locations":[{"physicalLocation":{"artifactLocation":{"uri":"a.go"}}}]}]}]}`)
	head := write("head.sarif", `{"runs":[{"tool":{"driver":{"rules":[{"id":"r3","properties":{"security-severity":"7.5"}}]}},"results":[
	  {"ruleId":"r1","level":"error","message":{"text":"m"},"locations":[{"physicalLocation":{"artifactLocation":{"uri":"a.go"}}}]},
	  {"ruleId":"r2","level":"error","message":{"text":"m"},"locations":[{"physicalLocation":{"artifactLocation":{"uri":"a.go"}}}]},
	  {"ruleId":"r3","level":"warning","message":{"text":"m"},"locations":[{"physicalLocation":{"artifactLocation":{"uri":"b.go"}}}]},
	  {"ruleId":"r4","level":"warning","message":{"text":"m"},"locations":[{"physicalLocation":{"artifactLocation":{"uri":"b.go"}}}]}]}]}`)
	covBase, covHead := write("cb", "81.5\n"), write("ch", "80.0%\n")

	o := opts(t, "shadow", "opened")
	o.SarifBase, o.SarifHead, o.CoverageBase, o.CoverageHead = base, head, covBase, covHead
	yes := true
	o.APIBreak = &yes
	rec, err := run.Run(context.Background(), o, fake(nil, "src/a.go"))
	if err != nil {
		t.Fatal(err)
	}
	i := rec.Inputs
	if i.SastNewHigh == nil || *i.SastNewHigh != 2 {
		t.Fatalf("sast = %v", i.SastNewHigh)
	}
	if i.CoverageDelta == nil || *i.CoverageDelta > -1.49 || *i.CoverageDelta < -1.51 {
		t.Fatalf("coverage = %v", i.CoverageDelta)
	}
	if i.APIBreak == nil || !*i.APIBreak || rec.DetTier != 2 {
		t.Fatalf("api = %v det %d", i.APIBreak, rec.DetTier)
	}
	for _, u := range rec.Unavailable {
		if u == "sast_new_high" || u == "coverage_delta" || u == "api_break" {
			t.Fatalf("unavailable = %v", rec.Unavailable)
		}
	}

	o.SarifHead = ""
	rec, _ = run.Run(context.Background(), o, fake(nil, "src/a.go"))
	if rec.Inputs.SastNewHigh != nil {
		t.Fatal("sast computed from a single file")
	}
}

func TestRecordedOnlyRepoNeverRequiresAttestation(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"shadow"} {
		o := opts(t, mode, "opened")
		if err := os.WriteFile(o.TiersPath, []byte("version: 1\ndefault_tier: 1\ntiers:\n  2:\n    paths: [\"auth/**\"]\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		rec, err := run.Run(context.Background(), o, fake(nil, "auth/a.go"))
		if err != nil {
			t.Fatal(err)
		}
		if rec.DetTier != 2 || rec.Attestation.Required || rec.Check.WouldBlock || rec.Attestation.Label != "tier2-self-reviewed" {
			t.Fatalf("rec = %+v %+v", rec.Attestation, rec.Check)
		}
	}
}

func TestFileListIncompleteFloor(t *testing.T) {
	t.Parallel()
	f := fake(nil, "src/a.go")
	f.PR.ChangedFiles = 3001
	rec, err := run.Run(context.Background(), opts(t, "shadow", "opened"), f)
	if err != nil {
		t.Fatal(err)
	}
	if rec.DetTier != 2 || len(rec.Floors) == 0 || rec.Floors[len(rec.Floors)-1].Rule != "file_list_incomplete" {
		t.Fatalf("floors = %+v det %d", rec.Floors, rec.DetTier)
	}
	f.PR.ChangedFiles = 1
	rec, _ = run.Run(context.Background(), opts(t, "shadow", "opened"), f)
	if rec.DetTier != 1 {
		t.Fatalf("complete list raised tier: %d", rec.DetTier)
	}
}

func TestStaleAttestationLabel(t *testing.T) {
	t.Parallel()
	labelAt := time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)
	before := func(h int) time.Time { return time.Date(2026, 11, 24, h, 0, 0, 0, time.UTC) }
	ptrT := func(t time.Time) *time.Time { return &t }
	tests := []struct {
		name   string
		commit time.Time
		force  *time.Time
		fresh  bool
	}{
		{"label after commit", before(8), nil, true},
		{"commit after label", before(10), nil, false},
		{"commit at same instant", labelAt, nil, false},
		{"force push after label", before(8), ptrT(before(10)), false},
		{"force push before label", before(7), ptrT(before(8)), true},
	}
	for _, tc := range tests {
		for _, mode := range []string{"shadow", "enforce"} {
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				t.Parallel()
				f := fake([]string{"attested"}, "auth/a.go")
				f.LabelEv = &gh.LabelEvent{Actor: "boss", At: labelAt}
				f.CommitDate, f.ForcePush = tc.commit, tc.force
				rec, err := run.Run(context.Background(), opts(t, mode, "labeled"), f)
				if err != nil {
					t.Fatal(err)
				}
				if rec.Attestation.Attested != tc.fresh || rec.Check.WouldBlock == tc.fresh {
					t.Fatalf("attested %v would_block %v", rec.Attestation.Attested, rec.Check.WouldBlock)
				}
				wantConclusion := "success"
				if mode == "enforce" && !tc.fresh {
					wantConclusion = "failure"
				}
				if rec.Check.Conclusion != wantConclusion {
					t.Fatalf("conclusion %s", rec.Check.Conclusion)
				}
			})
		}
	}
}

func TestLabelRemovalFailureFailsEnforce(t *testing.T) {
	t.Parallel()
	label := gh.LabelEvent{Actor: "boss", At: time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)}
	for _, files := range [][]string{{"auth/a.go"}, {"src/a.go"}} {
		f := fake([]string{"attested"}, files...)
		f.LabelEv = &label
		f.RemoveErr = io.ErrUnexpectedEOF
		rec, err := run.Run(context.Background(), opts(t, "enforce", "synchronize"), f)
		if err != nil {
			t.Fatal(err)
		}
		if rec.Check.Conclusion != "failure" || !rec.Check.WouldBlock || rec.Attestation.Attested {
			t.Fatalf("%v: check %+v attested %v", files, rec.Check, rec.Attestation.Attested)
		}
	}
	f := fake([]string{"attested"}, "auth/a.go")
	f.LabelEv = &label
	f.RemoveErr = io.ErrUnexpectedEOF
	rec, _ := run.Run(context.Background(), opts(t, "shadow", "synchronize"), f)
	if rec.Check.Conclusion != "success" {
		t.Fatalf("shadow conclusion %s", rec.Check.Conclusion)
	}
}

func TestTenureLookupFailureBlocksTier0(t *testing.T) {
	t.Parallel()
	doc := "version: 1\ndefault_tier: 1\ntiers:\n  0:\n    paths: [\"docs/**\"]\n"
	f := fake(nil, "docs/a.md")
	o := opts(t, "shadow", "opened")
	if err := os.WriteFile(o.TiersPath, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	rec, err := run.Run(context.Background(), o, f)
	if err != nil || rec.DetTier != 0 {
		t.Fatalf("baseline: det %d err %v", rec.DetTier, err)
	}
	f.CommitsErr = io.ErrUnexpectedEOF
	rec, err = run.Run(context.Background(), o, f)
	if err != nil {
		t.Fatal(err)
	}
	if rec.DetTier != 1 || rec.Inputs.AuthorCommits180d != nil || rec.Inputs.FirstTimeContributor != nil {
		t.Fatalf("det %d inputs %+v", rec.DetTier, rec.Inputs)
	}
	var found bool
	for _, u := range rec.Unavailable {
		found = found || u == "author_commits_180d"
	}
	if !found {
		t.Fatalf("unavailable = %v", rec.Unavailable)
	}
}
