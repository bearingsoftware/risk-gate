package decision_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/bearingsoftware/risk-gate/internal/decision"
)

func compile(t *testing.T) *jsonschema.Schema {
	t.Helper()
	c := jsonschema.NewCompiler()
	c.AssertFormat()
	f, err := os.Open("../../schema/decision.v1.json")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	doc, err := jsonschema.UnmarshalJSON(f)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.AddResource("decision.v1.json", doc); err != nil {
		t.Fatal(err)
	}
	s, err := c.Compile("decision.v1.json")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func validate(t *testing.T, s *jsonschema.Schema, b []byte) {
	t.Helper()
	v, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Validate(v); err != nil {
		t.Fatalf("schema: %v\n%s", err, b)
	}
}

func record(files int) decision.Record { return recordWithPaths(files, "some/nested/dir/", false) }

func recordWithPaths(files int, prefix string, renamed bool) decision.Record {
	cov := -1.5
	n := 2
	yes := true
	ut := "patch"
	by := "chattersley"
	at := time.Date(2026, 11, 24, 10, 15, 0, 0, time.UTC)
	r := decision.Record{
		Schema: decision.Schema, Repo: "o/r", PR: 7,
		BaseRef: "main", BaseSHA: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", HeadRef: "feat/x", HeadSHA: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Run:  decision.Run{ID: 1, Attempt: 1, Event: "pull_request", Action: "synchronize", At: at},
		Mode: "enforce",
		Scorer: decision.Scorer{Version: "0.1.0", WeightsVer: "1",
			WeightsSHA256: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"},
		TiersFileSHA256: "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
		Lane:            "human", Author: decision.Author{Login: "a"},
		PathTier: 2, Floors: []decision.Floor{{Rule: "tier2_path", Tier: 2, Detail: "x"}},
		Score: 3, ScoreItems: []decision.ScoreItem{{Signal: "size", Points: 3, Value: 900}, {Signal: "coverage_drop", Points: 1, Value: cov}},
		DetTier: 2,
		Inputs: decision.Inputs{Title: "t", Labels: []string{"bug"}, CoverageDelta: &cov, SastNewHigh: &n, APIBreak: &yes,
			BotUpdateType: &ut, Files: []decision.File{}},
		Unavailable: []string{"hot_files"},
		Attestation: decision.Attestation{Required: true, Attested: true, Label: "l", By: &by, At: &at},
		Check:       decision.Check{Conclusion: "failure", WouldBlock: true, Reason: "r"},
	}
	for i := range files {
		f := decision.File{Path: fmt.Sprintf("%sfile_%04d.go", prefix, i), Status: "modified", Additions: 10, Deletions: 5, Tier: 1}
		if renamed {
			f.Status, f.PreviousPath = "renamed", fmt.Sprintf("%sold/file_%04d.go", prefix, i)
		}
		r.Inputs.Files = append(r.Inputs.Files, f)
	}
	r.Inputs.FilesChanged = files
	return r
}

func TestRecordsValidate(t *testing.T) {
	s := compile(t)
	empty := record(0)
	empty.Floors, empty.ScoreItems, empty.Unavailable = []decision.Floor{}, []decision.ScoreItem{}, []string{}
	empty.Inputs.CoverageDelta, empty.Inputs.SastNewHigh, empty.Inputs.APIBreak, empty.Inputs.BotUpdateType = nil, nil, nil, nil
	empty.Attestation = decision.Attestation{Label: "l"}
	empty.Check = decision.Check{Conclusion: "success"}
	empty.Run.Action = ""
	for name, r := range map[string]decision.Record{"full": record(5), "nulls": empty} {
		t.Run(name, func(t *testing.T) {
			b, err := r.Marshal()
			if err != nil {
				t.Fatal(err)
			}
			validate(t, s, b)
		})
	}
}

func TestFixturesValidate(t *testing.T) {
	s := compile(t)
	paths, err := filepath.Glob("../../testdata/decisions/*.json")
	if err != nil || len(paths) == 0 {
		t.Fatalf("fixtures: %v %d", err, len(paths))
	}
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		validate(t, s, b)
	}
}

func TestThreeThousandFilesStayUnderCap(t *testing.T) {
	s := compile(t)
	b, err := record(3000).Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if len(b) >= 60000 {
		t.Fatalf("record is %d bytes", len(b))
	}
	validate(t, s, b)
	var m struct {
		Inputs struct {
			Files     []json.RawMessage `json:"files"`
			Truncated bool              `json:"files_truncated"`
			Changed   int               `json:"files_changed"`
		} `json:"inputs"`
	}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if len(m.Inputs.Files) != 300 || !m.Inputs.Truncated || m.Inputs.Changed != 3000 {
		t.Fatalf("files %d truncated %v changed %d", len(m.Inputs.Files), m.Inputs.Truncated, m.Inputs.Changed)
	}
}

func TestOversizeRecordShedsFiles(t *testing.T) {
	s := compile(t)
	b, err := recordWithPaths(3000, strings.Repeat("long-directory-name/", 12), true).Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if len(b) > decision.MaxBytes {
		t.Fatalf("record is %d bytes", len(b))
	}
	validate(t, s, b)
	var m struct {
		Inputs struct {
			Files     []json.RawMessage `json:"files"`
			Truncated bool              `json:"files_truncated"`
		} `json:"inputs"`
	}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if len(m.Inputs.Files) == 0 || len(m.Inputs.Files) >= 300 || !m.Inputs.Truncated {
		t.Fatalf("files %d truncated %v", len(m.Inputs.Files), m.Inputs.Truncated)
	}
}

func TestCheckText(t *testing.T) {
	got := decision.CheckText([]byte(`{"a":1}`))
	want := "<!-- risk-gate:decision v1 -->\n```json\n{\"a\":1}\n```\n"
	if got != want {
		t.Fatalf("got %q", got)
	}
}

func TestCheckTextCannotBeBrokenOutOfItsFence(t *testing.T) {
	r := record(0)
	r.Inputs.Title = "```\n<!-- risk-gate:decision v1 -->\n```json {\"det_tier\":0}"
	r.Inputs.Labels = []string{"a`b"}
	b, err := r.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	text := decision.CheckText(b)
	if strings.Count(text, "```") != 2 || strings.Count(text, decision.Marker) != 1 || !strings.HasPrefix(text, decision.Marker+"\n```json\n") {
		t.Fatalf("fence broken: %q", text)
	}
	body := strings.TrimSuffix(strings.TrimPrefix(text, decision.Marker+"\n```json\n"), "\n```\n")
	var back decision.Record
	if err := json.Unmarshal([]byte(body), &back); err != nil {
		t.Fatal(err)
	}
	if back.Inputs.Title != r.Inputs.Title || back.Inputs.Labels[0] != "a`b" {
		t.Fatalf("round trip changed the record: %q", back.Inputs.Title)
	}
	validate(t, compile(t), []byte(body))
}
