package score_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/bearingsoftware/risk-gate/internal/decision"
	"github.com/bearingsoftware/risk-gate/internal/gh"
	"github.com/bearingsoftware/risk-gate/internal/ghfake"
	"github.com/bearingsoftware/risk-gate/internal/run"
)

const (
	sha40a = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	sha40b = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

const goldenTiers = `version: 1
owner: "@chattersley"
default_tier: 1
tiers:
  2:
    paths: ["backend/internal/auth/**", ".github/workflows/**"]
  0:
    paths: ["**/*.md", "docs/**", "**/package.json", "**/package-lock.json"]
    bot_authors: ["renovate[bot]"]
    bot_conditions: {update_type: patch}
release:
  heads: ["develop", "release-please--*"]
attestation:
  label: tier2-self-reviewed
  permitted: ["chattersley"]
`

const goldenTiersNoWorkflows = `version: 1
owner: "@chattersley"
default_tier: 1
tiers:
  2:
    paths: ["backend/internal/auth/**"]
attestation:
  label: tier2-self-reviewed
  permitted: ["chattersley"]
`

func f(path string, add, del int) gh.File {
	return gh.File{Path: path, Status: "modified", Additions: add, Deletions: del}
}

type scenario struct {
	fixture string
	tiers   string
	fake    *ghfake.Fake
	opts    run.Options
	sarif   bool
	// fixture fields that disagree with the brief and are left out of the comparison
	ignore []string
}

func renovateBody() string {
	return "| Package | Change | Age | Confidence |\n|---|---|---|---|\n| [vitest](https://vitest.dev) | [`3.4.1` -> `3.4.2`](https://x) | a | b |\n"
}

func scenarios() []scenario {
	at := func(s string) func() time.Time {
		return func() time.Time { t, _ := time.Parse(time.RFC3339, s); return t }
	}
	pr := func(n int, title, author, baseRef, headRef string, labels ...string) gh.PullRequest {
		return gh.PullRequest{Number: n, Title: title, Author: author, AuthorBot: strings.HasSuffix(author, "[bot]"),
			BaseRef: baseRef, BaseSHA: sha40a, HeadRef: headRef, HeadSHA: sha40b, Labels: append([]string{}, labels...)}
	}

	// tier2-auth
	auth := &ghfake.Fake{
		PR: pr(1301, "fix(SECREPO-900): reject expired refresh tokens", "chattersley", "develop", "fix/secrepo-900-token-expiry", "bug"),
		FileList: []gh.File{
			f("backend/internal/auth/session.go", 40, 12), f("backend/internal/api/handlers.go", 20, 5),
			f("backend/internal/auth/session_test.go", 60, 20), f("backend/internal/api/handlers_test.go", 30, 10),
			f("backend/internal/store/a.go", 20, 5), f("backend/internal/store/b.go", 20, 10), f("backend/internal/store/c.go", 11, 5),
		},
		CommitsSince: map[bool]int{true: 412, false: 412},
	}
	// tier1-small-human
	small := &ghfake.Fake{
		PR:           pr(1302, "feat(SECREPO-901): sort package list by name", "chattersley", "develop", "feat/secrepo-901-list-sort"),
		FileList:     []gh.File{f("web/src/pages/Packages.tsx", 40, 10), f("web/src/pages/Packages.test.tsx", 25, 5), f("web/src/pages/sort.ts", 5, 3)},
		CommitsSince: map[bool]int{true: 412, false: 412},
	}
	// tier0-renovate
	renovate := &ghfake.Fake{
		PR:       pr(1303, "chore(deps): update dependency vitest to v3.4.2", "renovate[bot]", "develop", "renovate/vitest-monorepo"),
		FileList: []gh.File{f("web/package.json", 1, 1), f("web/package-lock.json", 11, 11)},
	}
	renovate.PR.Body = renovateBody()

	// release-lane: 41 files, 1800+410 lines, 700 test lines, 4 top-level dirs, 9 hot files
	rel := &ghfake.Fake{
		PR:           pr(1304, "Release: develop → main", "chattersley", "main", "develop"),
		CommitsSince: map[bool]int{true: 412, false: 412},
	}
	rel.FileList = append(rel.FileList, f(".github/workflows/ci.yml", 4, 2))
	for i := range 5 {
		rel.FileList = append(rel.FileList, f(fmt.Sprintf("backend/src/f%d_test.go", i), 100, 40))
	}
	roots := []string{"backend/src", "web/src", "docs"}
	var hot []string
	for i := range 35 {
		add, del := 37, 5
		if i == 0 {
			add, del = 38, 38
		}
		p := fmt.Sprintf("%s/n%02d.go", roots[i%3], i)
		rel.FileList = append(rel.FileList, f(p, add, del))
		if i < 9 {
			hot = append(hot, p)
		}
	}
	for range 4 {
		rel.Merged.Files = append(rel.Merged.Files, hot)
	}
	rel.Merged.Examined = 4

	// enforce-attested
	att := &ghfake.Fake{
		PR:           pr(80, "feat: parallel runner", "chattersley", "main", "feat/ctf-80-runner", "tier2-self-reviewed"),
		CommitsSince: map[bool]int{true: 57, false: 57},
		LabelEv:      &gh.LabelEvent{Actor: "chattersley", At: time.Date(2027, 1, 5, 9, 29, 41, 0, time.UTC)},
	}
	att.PR.Labels = []string{"tier2-self-reviewed"}
	att.FileList = []gh.File{
		f(".github/workflows/release.yml", 3, 1),
		{Path: "src/runner.ts", PreviousPath: "src/run.ts", Status: "renamed", Additions: 200, Deletions: 40},
		f("package.json", 5, 1),
		f("src/runner.test.ts", 250, 50),
	}
	for i := range 6 {
		del := 2
		if i == 5 {
			del = 1
		}
		att.FileList = append(att.FileList, f(fmt.Sprintf("src/a%d.ts", i), 57, del))
	}

	mk := func(id int64, event, action, mode, repo string, now string) run.Options {
		return run.Options{Repo: repo, Mode: mode, RunID: id, RunAttempt: 1, Event: event, EventAction: action, Now: at(now)}
	}
	return []scenario{
		{"tier2-auth-path.json", goldenTiers, auth, mk(19000000001, "pull_request", "synchronize", "shadow", "bearingsoftware/secure-repo", "2026-11-24T10:15:00Z"), true, []string{"check.would_block"}},
		{"tier1-small-human.json", goldenTiers, small, mk(19000000001, "pull_request", "synchronize", "shadow", "bearingsoftware/secure-repo", "2026-11-24T10:15:00Z"), true, nil},
		{"tier0-renovate-patch.json", goldenTiers, renovate, mk(19000000001, "pull_request", "synchronize", "shadow", "bearingsoftware/secure-repo", "2026-11-24T10:15:00Z"), false, []string{"unavailable:sast_new_high"}},
		{"release-lane.json", goldenTiers, rel, mk(19000000001, "pull_request", "synchronize", "shadow", "bearingsoftware/secure-repo", "2026-11-24T10:15:00Z"), false, []string{"check.would_block", "unavailable:sast_new_high"}},
		{"enforce-attested.json", goldenTiersNoWorkflows, att, mk(21000000002, "pull_request", "labeled", "enforce", "bearingsoftware/component-test-framework", "2027-01-05T09:30:00Z"), false, []string{"inputs.labels"}},
	}
}

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func asMap(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func del(m map[string]any, path ...string) {
	for _, k := range path[:len(path)-1] {
		m, _ = m[k].(map[string]any)
		if m == nil {
			return
		}
	}
	delete(m, path[len(path)-1])
}

func TestGolden(t *testing.T) {
	for _, sc := range scenarios() {
		t.Run(sc.fixture, func(t *testing.T) {
			dir := t.TempDir()
			sc.opts.PR = sc.fake.PR.Number
			sc.opts.TiersPath = writeFile(t, dir, "tiers.yml", sc.tiers)
			sc.opts.WeightsPath = "../../weights/v1.yml"
			sc.opts.Log = log.New(os.Stderr, "", 0)
			if sc.sarif {
				empty := writeFile(t, dir, "empty.sarif", `{"runs":[{"tool":{"driver":{"name":"x"}},"results":[]}]}`)
				sc.opts.SarifBase, sc.opts.SarifHead = empty, empty
			}
			rec, err := run.Run(context.Background(), sc.opts, sc.fake)
			if err != nil {
				t.Fatal(err)
			}
			got, err := json.Marshal(rec)
			if err != nil {
				t.Fatal(err)
			}
			want, err := os.ReadFile("../../testdata/decisions/" + sc.fixture)
			if err != nil {
				t.Fatal(err)
			}
			validateAgainstSchema(t, got)
			g, w := asMap(t, got), asMap(t, want)

			for _, m := range []map[string]any{g, w} {
				del(m, "inputs", "files")
				del(m, "scorer", "weights_sha256")
				del(m, "tiers_file_sha256")
				del(m, "check", "reason")
				for _, ig := range sc.ignore {
					if rest, ok := strings.CutPrefix(ig, "unavailable:"); ok {
						m["unavailable"] = slices.DeleteFunc(toStrings(m["unavailable"]), func(s string) bool { return s == rest })
						if rest == "sast_new_high" {
							del(m, "inputs", "sast_new_high")
						}
						continue
					}
					del(m, strings.Split(ig, ".")...)
				}
				u := toStrings(m["unavailable"])
				slices.Sort(u)
				m["unavailable"] = u
			}
			if !reflect.DeepEqual(g, w) {
				gj, _ := json.MarshalIndent(g, "", " ")
				wj, _ := json.MarshalIndent(w, "", " ")
				t.Fatalf("decision differs from fixture\n--- got\n%s\n--- want\n%s", gj, wj)
			}
		})
	}
}

func validateAgainstSchema(t *testing.T, b []byte) {
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
	sch, err := c.Compile("decision.v1.json")
	if err != nil {
		t.Fatal(err)
	}
	v, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	if err := sch.Validate(v); err != nil {
		t.Fatalf("produced record violates schema: %v", err)
	}
}

func toStrings(v any) []string {
	out := []string{}
	switch s := v.(type) {
	case []string:
		return s
	case []any:
		for _, e := range s {
			out = append(out, e.(string))
		}
	}
	return out
}

func TestFixtureAbbreviatedFilesAreSubsetOfProduced(t *testing.T) {
	for _, sc := range scenarios() {
		t.Run(sc.fixture, func(t *testing.T) {
			dir := t.TempDir()
			sc.opts.PR = sc.fake.PR.Number
			sc.opts.TiersPath = writeFile(t, dir, "tiers.yml", sc.tiers)
			sc.opts.WeightsPath = "../../weights/v1.yml"
			sc.opts.Log = log.New(os.Stderr, "", 0)
			rec, err := run.Run(context.Background(), sc.opts, sc.fake)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile("../../testdata/decisions/" + sc.fixture)
			if err != nil {
				t.Fatal(err)
			}
			var fx decision.Record
			if err := json.Unmarshal(raw, &fx); err != nil {
				t.Fatal(err)
			}
			if len(fx.Inputs.Files) > len(rec.Inputs.Files) || fx.Inputs.FilesChanged != len(rec.Inputs.Files) {
				t.Fatalf("fixture files %d, files_changed %d, produced %d", len(fx.Inputs.Files), fx.Inputs.FilesChanged, len(rec.Inputs.Files))
			}
			produced := map[string]decision.File{}
			for _, p := range rec.Inputs.Files {
				produced[p.Path] = p
			}
			for _, want := range fx.Inputs.Files {
				if got := produced[want.Path]; got != want {
					t.Errorf("file %s: got %+v, want %+v", want.Path, got, want)
				}
			}
			if rec.Inputs.FilesTruncated {
				t.Error("unexpected truncation")
			}
		})
	}
}
