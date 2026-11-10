package score

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/bearingsoftware/risk-gate/internal/tiers"
)

const tiersYAML = `version: 1
owner: "@x"
default_tier: 1
tiers:
  2:
    paths: ["backend/internal/auth/**", "infra/**"]
  0:
    paths: ["**/*.md", "docs/**"]
    bot_authors: ["renovate[bot]"]
    bot_conditions: {update_type: patch}
release:
  heads: ["develop", "release-please--*"]
`

func mustTiers(t testing.TB, doc string) *tiers.Config {
	t.Helper()
	c, err := tiers.Parse([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func mustWeights(t testing.TB) *Weights {
	t.Helper()
	b, err := os.ReadFile("../../weights/v1.yml")
	if err != nil {
		t.Fatal(err)
	}
	w, err := ParseWeights(b)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func ptr[T any](v T) *T { return &v }

func base(t testing.TB) Input {
	return Input{
		Title: "feat: thing", Author: "alice", HeadRef: "feat/x",
		Files:                []File{{Path: "src/a.go", Status: "modified", Additions: 5, Deletions: 5}},
		SastNewHigh:          ptr(0),
		CoverageDelta:        ptr(0.0),
		APIBreak:             ptr(false),
		AuthorCommits180d:    ptr(50),
		FirstTimeContributor: ptr(false),
		HotFiles:             ptr(0),
		Tiers:                mustTiers(t, tiersYAML),
		Weights:              mustWeights(t),
	}
}

func lines(path string, n int) File { return File{Path: path, Status: "modified", Additions: n} }

func floorRules(r Result) []string {
	var out []string
	for _, f := range r.Floors {
		out = append(out, f.Rule)
	}
	return out
}

func itemPoints(r Result, signal string) int {
	for _, i := range r.ScoreItems {
		if i.Signal == signal {
			return i.Points
		}
	}
	return 0
}

func TestSignals(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(*Input)
		signal string
		points int
	}{
		{"size 50", func(i *Input) { i.Files = []File{lines("a.go", 50)} }, "size", 0},
		{"size 51", func(i *Input) { i.Files = []File{lines("a.go", 51)} }, "size", 1},
		{"size 200", func(i *Input) { i.Files = []File{lines("a_test.go", 100), lines("a.go", 100)} }, "size", 1},
		{"size 201", func(i *Input) { i.Files = []File{lines("a_test.go", 100), lines("a.go", 101)} }, "size", 2},
		{"size 500", func(i *Input) { i.Files = []File{lines("a_test.go", 250), lines("a.go", 250)} }, "size", 2},
		{"size 501", func(i *Input) { i.Files = []File{lines("a_test.go", 250), lines("a.go", 251)} }, "size", 3},
		{"files 5", func(i *Input) { i.Files = nFiles(5) }, "diffusion_files", 0},
		{"files 6", func(i *Input) { i.Files = nFiles(6) }, "diffusion_files", 1},
		{"dirs 2", func(i *Input) { i.Files = []File{lines("a/x.go", 1), lines("b/x.go", 1)} }, "diffusion_dirs", 0},
		{"dirs 3", func(i *Input) { i.Files = []File{lines("a/x.go", 1), lines("b/x.go", 1), lines("c/x.go", 1)} }, "diffusion_dirs", 1},
		{"root files are not dirs", func(i *Input) { i.Files = []File{lines("a.go", 1), lines("b.go", 1), lines("c.go", 1)} }, "diffusion_dirs", 0},
		{"no tests 50", func(i *Input) { i.Files = []File{lines("a.go", 50)} }, "no_tests", 0},
		{"no tests 51", func(i *Input) { i.Files = []File{lines("a.go", 51)} }, "no_tests", 2},
		{"tests present", func(i *Input) { i.Files = []File{lines("a.go", 51), lines("a_test.go", 1)} }, "no_tests", 0},
		{"bug label", func(i *Input) { i.Labels = []string{"bug"} }, "bug_fix", 2},
		{"fix colon", func(i *Input) { i.Title = "fix: x" }, "bug_fix", 2},
		{"fix scope", func(i *Input) { i.Title = "fix(api): x" }, "bug_fix", 2},
		{"fixture is not fix", func(i *Input) { i.Title = "fixture: x" }, "bug_fix", 0},
		{"feat", func(i *Input) { i.Title = "feat(fix): x" }, "bug_fix", 0},
		{"revert title", func(i *Input) { i.Title = `Revert "feat: x"` }, "revert_or_hotfix", 2},
		{"hotfix label", func(i *Input) { i.Labels = []string{"hotfix"} }, "revert_or_hotfix", 2},
		{"coverage drop", func(i *Input) { i.CoverageDelta = ptr(-0.1) }, "coverage_drop", 1},
		{"coverage flat", func(i *Input) { i.CoverageDelta = ptr(0.0) }, "coverage_drop", 0},
		{"coverage unknown", func(i *Input) { i.CoverageDelta = nil }, "coverage_drop", 0},
		{"tenure 4", func(i *Input) { i.AuthorCommits180d = ptr(4) }, "author_tenure", 2},
		{"tenure 0", func(i *Input) { i.AuthorCommits180d = ptr(0) }, "author_tenure", 2},
		{"tenure 5", func(i *Input) { i.AuthorCommits180d = ptr(5) }, "author_tenure", 0},
		{"tenure unknown", func(i *Input) { i.AuthorCommits180d = nil }, "author_tenure", 0},
		{"hot 1", func(i *Input) { i.HotFiles = ptr(1) }, "hot_files", 1},
		{"hot 0", func(i *Input) { i.HotFiles = ptr(0) }, "hot_files", 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			in := base(t)
			tc.mutate(&in)
			if got := itemPoints(Evaluate(in), tc.signal); got != tc.points {
				t.Fatalf("%s points = %d, want %d", tc.signal, got, tc.points)
			}
		})
	}
}

func nFiles(n int) []File {
	var fs []File
	for i := range n {
		fs = append(fs, lines(fmt.Sprintf("f%d.go", i), 1))
	}
	return fs
}

func TestRevertAddsFloor1AndPoints(t *testing.T) {
	t.Parallel()
	in := base(t)
	in.Title = `Revert "feat: x"`
	r := Evaluate(in)
	if !slices.Equal(floorRules(r), []string{"revert_or_hotfix"}) || r.Floors[0].Tier != 1 {
		t.Fatalf("floors = %+v", r.Floors)
	}
	if r.DetTier != 1 {
		t.Fatalf("det = %d", r.DetTier)
	}
}

func TestFloors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(*Input)
		rule   string
		want   bool
	}{
		{"tier2 path", func(i *Input) { i.Files = []File{lines("infra/main.tf", 1)} }, "tier2_path", true},
		{"tier2 rename out of auth", func(i *Input) {
			i.Files = []File{{Path: "backend/internal/other/s.go", PreviousPath: "backend/internal/auth/s.go", Status: "renamed", Additions: 1}}
		}, "tier2_path", true},
		{"tier2 rename into auth", func(i *Input) {
			i.Files = []File{{Path: "backend/internal/auth/s.go", PreviousPath: "x/s.go", Status: "renamed", Additions: 1}}
		}, "tier2_path", true},
		{"tier1 only", func(i *Input) {}, "tier2_path", false},
		{"workflow", func(i *Input) { i.Files = []File{lines(".github/workflows/ci.yml", 1)} }, "control_file", true},
		{"tiers file", func(i *Input) { i.Files = []File{lines(".github/review-tiers.yml", 1)} }, "control_file", true},
		{"codeowners", func(i *Input) { i.Files = []File{lines(".github/CODEOWNERS", 1)} }, "control_file", true},
		{"jenkinsfile", func(i *Input) { i.Files = []File{lines("Jenkinsfile", 1)} }, "control_file", true},
		{"nested jenkinsfile", func(i *Input) { i.Files = []File{lines("ci/Jenkinsfile", 1)} }, "control_file", true},
		{"workflow renamed away", func(i *Input) {
			i.Files = []File{{Path: "ci.yml", PreviousPath: ".github/workflows/ci.yml", Status: "renamed", Additions: 1}}
		}, "control_file", true},
		{"other github file", func(i *Input) { i.Files = []File{lines(".github/dependabot.yml", 1)} }, "control_file", false},
		{"sast new", func(i *Input) { i.SastNewHigh = ptr(1) }, "sast_new_high", true},
		{"sast none", func(i *Input) { i.SastNewHigh = ptr(0) }, "sast_new_high", false},
		{"sast unavailable", func(i *Input) { i.SastNewHigh = nil }, "sast_new_high", false},
		{"go.mod human", func(i *Input) { i.Files = []File{lines("go.mod", 1)} }, "dep_manifest_human", true},
		{"nested package.json human", func(i *Input) { i.Files = []File{lines("web/package.json", 1)} }, "dep_manifest_human", true},
		{"yarn.lock human", func(i *Input) { i.Files = []File{lines("yarn.lock", 1)} }, "dep_manifest_human", true},
		{"pnpm lock human", func(i *Input) { i.Files = []File{lines("pnpm-lock.yaml", 1)} }, "dep_manifest_human", true},
		{"go.mod bot", func(i *Input) { i.AuthorBot, i.Author = true, "renovate[bot]"; i.Files = []File{lines("go.mod", 1)} }, "dep_manifest_human", false},
		{"api break", func(i *Input) { i.APIBreak = ptr(true) }, "api_break", true},
		{"api ok", func(i *Input) { i.APIBreak = ptr(false) }, "api_break", false},
		{"api unavailable", func(i *Input) { i.APIBreak = nil }, "api_break", false},
		{"first timer", func(i *Input) { i.FirstTimeContributor = ptr(true) }, "first_time_contributor", true},
		{"first timer bot", func(i *Input) { i.AuthorBot = true; i.FirstTimeContributor = ptr(true) }, "first_time_contributor", false},
		{"returning", func(i *Input) { i.FirstTimeContributor = ptr(false) }, "first_time_contributor", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			in := base(t)
			tc.mutate(&in)
			r := Evaluate(in)
			got := slices.Contains(floorRules(r), tc.rule)
			if got != tc.want {
				t.Fatalf("floor %s = %v, want %v (floors %v)", tc.rule, got, tc.want, floorRules(r))
			}
			if got && r.DetTier < 2 && tc.rule != "revert_or_hotfix" {
				t.Fatalf("floor %s did not raise det_tier: %d", tc.rule, r.DetTier)
			}
		})
	}
}

func TestControlFileForcedEvenWhenTiersOmitsIt(t *testing.T) {
	t.Parallel()
	in := base(t)
	in.Files = []File{lines(".github/workflows/ci.yml", 1)}
	r := Evaluate(in)
	if r.PathTier != 2 || r.Inputs.Files[0].Tier != 2 {
		t.Fatalf("path tier %d, file tier %d", r.PathTier, r.Inputs.Files[0].Tier)
	}
	if !slices.Equal(floorRules(r), []string{"control_file"}) {
		t.Fatalf("floors = %v", floorRules(r))
	}
}

func TestFloorsNeverLowered(t *testing.T) {
	t.Parallel()
	in := base(t)
	in.Files = []File{lines("docs/a.md", 1)}
	in.APIBreak = ptr(true)
	r := Evaluate(in)
	if r.PathTier != 0 || r.Score != 0 || r.DetTier != 2 {
		t.Fatalf("path %d score %d det %d", r.PathTier, r.Score, r.DetTier)
	}
	in = base(t)
	in.Files = []File{lines("infra/a.tf", 1)}
	if r := Evaluate(in); r.DetTier != 2 || r.DetTier < r.PathTier {
		t.Fatalf("det = %d", r.DetTier)
	}
}

func TestMapping(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		mutate    func(*Input)
		wantScore int
		wantTier  int
	}{
		{"score 0 tier1 files", func(i *Input) {}, 0, 1},
		{"score 1", func(i *Input) { i.Files = []File{lines("src/a.go", 60), lines("src/a_test.go", 1)} }, 1, 1},
		{"score 2", func(i *Input) { i.Files = []File{lines("src/a.go", 300), lines("src/a_test.go", 1)} }, 2, 1},
		{"score 3", func(i *Input) { i.Files = []File{lines("src/a.go", 600), lines("src/a_test.go", 1)} }, 3, 1},
		{"score 5", func(i *Input) {
			i.Files = []File{lines("src/a.go", 600), lines("src/a_test.go", 1)}
			i.Labels = []string{"bug"}
		}, 5, 2},
		{"score exactly 4", func(i *Input) {
			i.Files = []File{lines("src/a.go", 60), lines("src/a_test.go", 1)}
			i.AuthorCommits180d = ptr(1)
			i.HotFiles = ptr(1)
		}, 4, 2},
		{"human tier0 docs only", func(i *Input) { i.Files = []File{lines("README.md", 3), lines("docs/x.txt", 3)} }, 0, 0},
		{"human tier0 docs score 1", func(i *Input) { i.Files = []File{lines("README.md", 59), lines("docs/a_test.go", 1)} }, 1, 0},
		{"human tier0 docs score 2 is tier1", func(i *Input) { i.Files = []File{lines("README.md", 299), lines("docs/a_test.go", 1)} }, 2, 1},
		{"human mixed docs and code", func(i *Input) { i.Files = []File{lines("README.md", 3), lines("src/a.go", 3)} }, 0, 1},
		{"human no files", func(i *Input) { i.Files = nil }, 0, 1},
		{"renamed out of auth", func(i *Input) {
			i.Files = []File{{Path: "docs/s.md", PreviousPath: "backend/internal/auth/s.md", Status: "renamed", Additions: 1}}
		}, 0, 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			in := base(t)
			tc.mutate(&in)
			r := Evaluate(in)
			if r.Score != tc.wantScore || r.DetTier != tc.wantTier {
				t.Fatalf("score %d det %d, want %d / %d (items %+v)", r.Score, r.DetTier, tc.wantScore, tc.wantTier, r.ScoreItems)
			}
		})
	}
}

func botInput(t testing.TB, body string, files ...File) Input {
	in := base(t)
	in.Author, in.AuthorBot = "renovate[bot]", true
	in.Title = "chore(deps): update x"
	in.Body = body
	in.Files = files
	in.AuthorCommits180d, in.FirstTimeContributor = nil, nil
	return in
}

func renovateBody(from, to string) string {
	return fmt.Sprintf("| Package | Change | Age |\n|---|---|---|\n| [x](https://x) | [`%s` -> `%s`](https://d) | ok |\n", from, to)
}

func TestBotMapping(t *testing.T) {
	t.Parallel()
	manifest := lines("web/package.json", 1)
	lock := lines("web/package-lock.json", 1)
	docs := lines("docs/a.md", 1)
	tests := []struct {
		name  string
		in    Input
		tier  int
		utype string
	}{
		{"patch manifests+docs", botInput(t, renovateBody("1.2.3", "1.2.4"), docs, manifest, lock), 0, "patch"},
		{"minor", botInput(t, renovateBody("1.2.3", "1.3.0"), docs), 1, "minor"},
		{"unknown", botInput(t, "nothing here", docs), 1, "unknown"},
		{"patch all tier 0", botInput(t, renovateBody("1.2.3", "1.2.4"), docs, docs), 0, "patch"},
		{"patch, code file", botInput(t, renovateBody("1.2.3", "1.2.4"), lines("src/a.go", 1)), 1, "patch"},
		{"patch, tier 2 file", botInput(t, renovateBody("1.2.3", "1.2.4"), lines("infra/a.tf", 1)), 2, "patch"},
		{"other bot", func() Input {
			i := botInput(t, renovateBody("1.2.3", "1.2.4"), docs)
			i.Author = "dependabot[bot]"
			return i
		}(), 1, "patch"},
		{"patch but score>0", botInput(t, renovateBody("1.2.3", "1.2.4"), lines("docs/a.md", 80)), 1, "patch"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := Evaluate(tc.in)
			if r.DetTier != tc.tier {
				t.Fatalf("det = %d, want %d (path %d score %d)", r.DetTier, tc.tier, r.PathTier, r.Score)
			}
			if r.Lane != "bot" || *r.Inputs.BotUpdateType != tc.utype {
				t.Fatalf("lane %s type %v", r.Lane, *r.Inputs.BotUpdateType)
			}
			if !slices.Contains(r.Unavailable, "author_commits_180d") || !slices.Contains(r.Unavailable, "first_time_contributor") {
				t.Fatalf("unavailable = %v", r.Unavailable)
			}
		})
	}
}

func TestBotTier0RequiresManifestsAsTierZero(t *testing.T) {
	t.Parallel()
	doc := strings.Replace(tiersYAML, `paths: ["**/*.md", "docs/**"]`, `paths: ["**/*.md", "docs/**", "**/package.json", "**/package-lock.json"]`, 1)
	in := botInput(t, renovateBody("1.2.3", "1.2.4"), lines("web/package.json", 1), lines("web/package-lock.json", 1))
	in.Tiers = mustTiers(t, doc)
	if r := Evaluate(in); r.DetTier != 0 {
		t.Fatalf("det = %d", r.DetTier)
	}
}

func TestLane(t *testing.T) {
	t.Parallel()
	tests := []struct {
		head string
		bot  bool
		want string
	}{
		{"develop", false, "release"},
		{"develop", true, "release"},
		{"release-please--branches--main", false, "release"},
		{"renovate/x", true, "bot"},
		{"feat/x", false, "human"},
	}
	for _, tc := range tests {
		in := base(t)
		in.HeadRef, in.AuthorBot = tc.head, tc.bot
		if got := Evaluate(in).Lane; got != tc.want {
			t.Errorf("%s bot=%v lane = %s, want %s", tc.head, tc.bot, got, tc.want)
		}
	}
}

func TestIsTest(t *testing.T) {
	t.Parallel()
	yes := []string{"a_test.go", "x/a_test.go", "a.test.ts", "a.test.tsx", "a.spec.ts", "a.spec.tsx", "src/__tests__/a.ts", "x/__component_tests__/y/a.ts", "__e2e_tests__/a.ts", "__integration_tests__/a.ts", "tests/a.py", "pkg/tests/a.py"}
	no := []string{"a.go", "a.test.js", "contest/a.go", "testsuite/a.go", "src/latest/a.go", "a_test.go.bak"}
	for _, p := range yes {
		if !isTest(p) {
			t.Errorf("isTest(%q) = false", p)
		}
	}
	for _, p := range no {
		if isTest(p) {
			t.Errorf("isTest(%q) = true", p)
		}
	}
}

func TestRenovateUpdateType(t *testing.T) {
	t.Parallel()
	row := func(c string) string { return "| Package | Change |\n|---|---|\n| x | " + c + " |\n" }
	tests := []struct{ name, body, want string }{
		{"patch", renovateBody("3.4.1", "3.4.2"), "patch"},
		{"minor", renovateBody("3.4.1", "3.5.0"), "minor"},
		{"major", renovateBody("3.4.1", "4.0.0"), "major"},
		{"arrow glyph", row("[`1.0.0` → `1.0.1`](u)"), "patch"},
		{"caret range", row("`^1.2.3` → `^1.2.4`"), "patch"},
		{"v prefix", row("`v1.2.3` → `v1.3.0`"), "minor"},
		{"two part", row("`1.2` → `1.3`"), "minor"},
		{"digest", row("`a1b2c3d` → `e4f5a6b`"), "digest"},
		{"sha256 digest", row("`sha256:abcdef1234` → `sha256:1234abcdef`"), "digest"},
		{"prerelease", row("`1.2.3-rc.1` → `1.2.3`"), "unknown"},
		{"identical", row("`1.2.3` → `1.2.3`"), "unknown"},
		{"no table", "Bumps x from 1.2.3 to 1.2.4", "unknown"},
		{"unparseable", row("something else"), "unknown"},
		{"widest wins", "| P | Change |\n|---|---|\n| a | `1.0.0` → `1.0.1` |\n| b | `1.0.0` → `2.0.0` |\n", "major"},
		{"any unknown poisons", "| P | Change |\n|---|---|\n| a | `1.0.0` → `1.0.1` |\n| b | junk |\n", "unknown"},
		{"change column not second", "| Package | Age | Change |\n|---|---|---|\n| a | x | `1.0.0` → `1.0.1` |\n", "patch"},
	}
	for _, tc := range tests {
		if got := ParseRenovateUpdateType(tc.body); got != tc.want {
			t.Errorf("%s: got %s, want %s", tc.name, got, tc.want)
		}
	}
}

func TestInputsAggregates(t *testing.T) {
	t.Parallel()
	in := base(t)
	in.Files = []File{
		{Path: "backend/a.go", Status: "modified", Additions: 10, Deletions: 2},
		{Path: "backend/a_test.go", Status: "modified", Additions: 5, Deletions: 1},
		{Path: "web/x.ts", PreviousPath: "web/y.ts", Status: "renamed", Additions: 1, Deletions: 1},
		{Path: "package.json", Status: "modified", Additions: 1, Deletions: 1},
	}
	r := Evaluate(in)
	i := r.Inputs
	if i.LinesAdded != 17 || i.LinesDeleted != 5 || i.TestLines != 6 || i.NonTestLines != 16 || i.FilesChanged != 4 || i.TopLevelDirs != 2 || !i.DepManifestChanged {
		t.Fatalf("inputs = %+v", i)
	}
	if i.Files[2].PreviousPath != "web/y.ts" || i.BotUpdateType != nil {
		t.Fatalf("files/bot = %+v", i)
	}
}

func TestManifestTierByAuthor(t *testing.T) {
	t.Parallel()
	bot := func(files ...File) Input { return botInput(t, renovateBody("1.2.3", "1.2.4"), files...) }
	human := func(files ...File) Input {
		in := base(t)
		in.Files = files
		return in
	}
	otherBot := bot(lines("go.mod", 1))
	otherBot.Author = "dependabot[bot]"
	tests := []struct {
		name     string
		in       Input
		path     int
		det      int
		fileTier []int
		floor    bool
	}{
		{"bot manifest", bot(lines("go.mod", 1), lines("go.sum", 1)), 0, 0, []int{0, 0}, false},
		{"bot nested lockfiles", bot(lines("web/pnpm-lock.yaml", 1), lines("a/b/yarn.lock", 1), lines("web/package.json", 1)), 0, 0, []int{0, 0, 0}, false},
		{"bot manifest and go file", bot(lines("go.mod", 1), lines("src/a.go", 1)), 1, 1, []int{0, 1}, false},
		{"human manifest", human(lines("go.mod", 1)), 1, 2, []int{1}, true},
		{"bot not in bot_authors", otherBot, 1, 1, []int{1}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := Evaluate(tc.in)
			if r.PathTier != tc.path || r.DetTier != tc.det {
				t.Fatalf("path %d det %d, want %d / %d", r.PathTier, r.DetTier, tc.path, tc.det)
			}
			for i, f := range r.Inputs.Files {
				if f.Tier != tc.fileTier[i] {
					t.Errorf("file %s tier %d, want %d", f.Path, f.Tier, tc.fileTier[i])
				}
			}
			if got := slices.Contains(floorRules(r), "dep_manifest_human"); got != tc.floor {
				t.Errorf("dep_manifest_human = %v", got)
			}
		})
	}
}

func TestFileListIncompleteFloor(t *testing.T) {
	t.Parallel()
	in := base(t)
	in.FileListIncomplete = true
	if r := Evaluate(in); r.DetTier != 2 || !slices.Contains(floorRules(r), "file_list_incomplete") {
		t.Fatalf("floors = %v det %d", floorRules(r), r.DetTier)
	}
}

func TestTier2BeatsBotManifestAndTier0(t *testing.T) {
	t.Parallel()
	in := botInput(t, renovateBody("1.2.3", "1.2.4"), lines("go.mod", 1), lines("infra/a.tf", 1))
	r := Evaluate(in)
	if r.PathTier != 2 || r.DetTier != 2 || r.Inputs.Files[0].Tier != 0 || r.Inputs.Files[1].Tier != 2 {
		t.Fatalf("path %d det %d files %+v", r.PathTier, r.DetTier, r.Inputs.Files)
	}

	doc := strings.Replace(tiersYAML, `paths: ["**/*.md", "docs/**"]`, `paths: ["**/*.md", "docs/**", "infra/**"]`, 1)
	for _, bot := range []bool{false, true} {
		in := base(t)
		in.Tiers = mustTiers(t, doc)
		in.Files = []File{lines("infra/readme.md", 1)}
		if bot {
			in = botInput(t, renovateBody("1.2.3", "1.2.4"), lines("infra/readme.md", 1))
			in.Tiers = mustTiers(t, doc)
		}
		if r := Evaluate(in); r.Inputs.Files[0].Tier != 2 || r.DetTier != 2 {
			t.Fatalf("bot=%v: tier 0 glob beat tier 2: file %d det %d", bot, r.Inputs.Files[0].Tier, r.DetTier)
		}
	}
}
