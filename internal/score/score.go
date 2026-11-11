// Package score computes the deterministic risk score, floors and tier for one PR.
package score

import (
	"path"
	"regexp"
	"strings"

	"github.com/bearingsoftware/risk-gate/internal/decision"
	"github.com/bearingsoftware/risk-gate/internal/tiers"
)

type File struct {
	Path         string
	PreviousPath string
	Status       string
	Additions    int
	Deletions    int
}

// Input is everything the scorer needs. A nil pointer means the signal could not be computed.
type Input struct {
	Title     string
	Body      string
	Labels    []string
	Author    string
	AuthorBot bool
	HeadRef   string
	Files     []File
	// FileListIncomplete is set when GitHub reported more changed files than it returned.
	FileListIncomplete bool

	SastNewHigh          *int
	CoverageDelta        *float64
	APIBreak             *bool
	AuthorCommits180d    *int
	FirstTimeContributor *bool
	HotFiles             *int

	Tiers   *tiers.Config
	Weights *Weights
}

// Result carries the decision fields the scorer owns.
type Result struct {
	Lane        string
	PathTier    int
	Floors      []decision.Floor
	Score       int
	ScoreItems  []decision.ScoreItem
	DetTier     int
	Inputs      decision.Inputs
	Unavailable []string
}

var controlGlobs = []string{".github/workflows/**", ".github/review-tiers.yml", ".github/CODEOWNERS", "Jenkinsfile"}

var depManifests = map[string]bool{
	"go.mod": true, "go.sum": true, "package.json": true,
	"package-lock.json": true, "pnpm-lock.yaml": true, "yarn.lock": true,
}

var lockfiles = map[string]bool{
	"go.sum": true, "package-lock.json": true, "pnpm-lock.yaml": true, "yarn.lock": true,
}

var testDirs = []string{"__tests__", "__component_tests__", "__e2e_tests__", "__integration_tests__", "tests"}

var fixTitle = regexp.MustCompile(`^fix(\([^)]*\))?!?:`)

func isControl(p string) bool {
	for _, g := range controlGlobs {
		if tiers.Match(g, p) {
			return true
		}
	}
	return false
}

func isManifest(p string) bool { return depManifests[path.Base(p)] }

func isLockfile(p string) bool { return lockfiles[path.Base(p)] }

func isCodeManifest(p string) bool { return isManifest(p) && !isLockfile(p) }

func isTest(p string) bool {
	base := path.Base(p)
	if strings.HasSuffix(base, "_test.go") {
		return true
	}
	for _, s := range []string{".test.ts", ".test.tsx", ".spec.ts", ".spec.tsx"} {
		if strings.HasSuffix(base, s) {
			return true
		}
	}
	for _, d := range strings.Split(path.Dir(p), "/") {
		for _, t := range testDirs {
			if d == t {
				return true
			}
		}
	}
	return false
}

func hasLabel(labels []string, want string) bool {
	for _, l := range labels {
		if strings.EqualFold(l, want) {
			return true
		}
	}
	return false
}

func Evaluate(in Input) Result {
	w, cfg := in.Weights, in.Tiers
	r := Result{Floors: []decision.Floor{}, ScoreItems: []decision.ScoreItem{}, Unavailable: []string{}}

	inputs := decision.Inputs{
		Title:  in.Title,
		Labels: append([]string{}, in.Labels...),
		Files:  make([]decision.File, 0, len(in.Files)),
	}
	dirs := map[string]bool{}
	var tier2Path, control, manifest, floorManifest string
	allTier0OrManifest := len(in.Files) > 0
	allTier0 := len(in.Files) > 0

	for _, f := range in.Files {
		tier := cfg.Tier(f.Path)
		if f.PreviousPath != "" {
			tier = max(tier, cfg.Tier(f.PreviousPath))
		}
		if in.AuthorBot && cfg.IsBotAuthor(in.Author) && isManifest(f.Path) && tier != 2 {
			tier = 0
		}
		ctrl := isControl(f.Path) || (f.PreviousPath != "" && isControl(f.PreviousPath))
		if ctrl {
			tier = 2
			if control == "" {
				control = f.Path
			}
		}
		if tier == 2 && !ctrl && tier2Path == "" {
			tier2Path = f.Path
		}
		r.PathTier = max(r.PathTier, tier)

		man := isManifest(f.Path) || (f.PreviousPath != "" && isManifest(f.PreviousPath))
		if man && manifest == "" {
			manifest = f.Path
		}
		floorMan := man
		if !w.lockfilesFloor() {
			floorMan = isCodeManifest(f.Path) || (f.PreviousPath != "" && isCodeManifest(f.PreviousPath))
		}
		if floorMan && floorManifest == "" {
			floorManifest = f.Path
		}
		if tier != 0 {
			allTier0 = false
			if !man {
				allTier0OrManifest = false
			}
		}

		lines := f.Additions + f.Deletions
		inputs.LinesAdded += f.Additions
		inputs.LinesDeleted += f.Deletions
		if isTest(f.Path) {
			inputs.TestLines += lines
		} else {
			inputs.NonTestLines += lines
		}
		if top, _, ok := strings.Cut(f.Path, "/"); ok {
			dirs[top] = true
		}
		inputs.Files = append(inputs.Files, decision.File{
			Path: f.Path, PreviousPath: f.PreviousPath, Status: f.Status,
			Additions: f.Additions, Deletions: f.Deletions, Tier: tier,
		})
	}
	inputs.FilesChanged = len(in.Files)
	inputs.TopLevelDirs = len(dirs)
	inputs.DepManifestChanged = manifest != ""
	inputs.CoverageDelta = in.CoverageDelta
	inputs.SastNewHigh = in.SastNewHigh
	inputs.APIBreak = in.APIBreak
	inputs.AuthorCommits180d = in.AuthorCommits180d
	inputs.FirstTimeContributor = in.FirstTimeContributor
	inputs.HotFiles = in.HotFiles

	if in.AuthorBot {
		ut := ParseRenovateUpdateType(in.Body)
		inputs.BotUpdateType = &ut
	}

	floor := func(rule string, tier int, detail string) {
		r.Floors = append(r.Floors, decision.Floor{Rule: rule, Tier: tier, Detail: detail})
	}
	item := func(signal string, points int, value any) {
		if points != 0 {
			r.Score += points
			r.ScoreItems = append(r.ScoreItems, decision.ScoreItem{Signal: signal, Points: points, Value: value})
		}
	}

	if tier2Path != "" {
		floor("tier2_path", 2, tier2Path)
	}
	if control != "" {
		floor("control_file", 2, control)
	}
	if in.SastNewHigh != nil && *in.SastNewHigh > 0 {
		floor("sast_new_high", 2, "")
	}
	if floorManifest != "" && !in.AuthorBot {
		floor("dep_manifest_human", 2, floorManifest)
	}
	if in.APIBreak != nil && *in.APIBreak {
		floor("api_break", 2, "")
	}
	revert := strings.HasPrefix(in.Title, `Revert "`) || hasLabel(in.Labels, "hotfix")
	if revert {
		floor("revert_or_hotfix", 1, "")
	}
	if !in.AuthorBot && in.FirstTimeContributor != nil && *in.FirstTimeContributor {
		floor("first_time_contributor", 2, "")
	}

	if in.FileListIncomplete {
		floor("file_list_incomplete", 2, "GitHub returned fewer files than changed_files")
	}

	lines := inputs.LinesAdded + inputs.LinesDeleted
	if revert {
		item("revert_or_hotfix", w.RevertHotfix.Points, nil)
	}
	if hasLabel(in.Labels, "bug") || fixTitle.MatchString(strings.ToLower(in.Title)) {
		item("bug_fix", w.BugFix.Points, nil)
	}
	item("size", w.sizePoints(lines), lines)
	if inputs.FilesChanged > w.DiffusionFiles.Over {
		item("diffusion_files", w.DiffusionFiles.Points, inputs.FilesChanged)
	}
	if inputs.TopLevelDirs > w.DiffusionDirs.Over {
		item("diffusion_dirs", w.DiffusionDirs.Points, inputs.TopLevelDirs)
	}
	if inputs.NonTestLines > w.NoTests.NonTestLinesOver && inputs.TestLines == 0 {
		item("no_tests", w.NoTests.Points, inputs.NonTestLines)
	}
	if in.CoverageDelta != nil && *in.CoverageDelta < 0 {
		item("coverage_drop", w.CoverageDrop.Points, *in.CoverageDelta)
	}
	if in.AuthorCommits180d != nil && *in.AuthorCommits180d < w.AuthorTenure.Commits180dUnder {
		item("author_tenure", w.AuthorTenure.Points, *in.AuthorCommits180d)
	}
	if in.HotFiles != nil && *in.HotFiles >= w.HotFiles.AtLeast {
		item("hot_files", w.HotFiles.Points, *in.HotFiles)
	}

	if in.SastNewHigh == nil {
		r.Unavailable = append(r.Unavailable, "sast_new_high")
	}
	if in.CoverageDelta == nil {
		r.Unavailable = append(r.Unavailable, "coverage_delta")
	}
	if in.APIBreak == nil {
		r.Unavailable = append(r.Unavailable, "api_break")
	}
	if in.AuthorCommits180d == nil {
		r.Unavailable = append(r.Unavailable, "author_commits_180d")
	}
	if in.FirstTimeContributor == nil {
		r.Unavailable = append(r.Unavailable, "first_time_contributor")
	}
	if in.HotFiles == nil {
		r.Unavailable = append(r.Unavailable, "hot_files")
	}

	switch {
	case cfg.IsReleaseHead(in.HeadRef):
		r.Lane = "release"
	case in.AuthorBot:
		r.Lane = "bot"
	default:
		r.Lane = "human"
	}

	mapped := 2
	if r.Score <= w.Mapping.Tier1MaxScore {
		mapped = 1
	}
	if in.AuthorBot {
		if cfg.IsBotAuthor(in.Author) && allTier0OrManifest && r.Score == 0 && botTypeOK(cfg, *inputs.BotUpdateType) {
			mapped = 0
		}
	} else if r.Score <= w.Mapping.HumanTier0MaxScore && allTier0 && in.AuthorCommits180d != nil && in.FirstTimeContributor != nil {
		mapped = 0
	}
	r.DetTier = max(mapped, r.PathTier)
	for _, f := range r.Floors {
		r.DetTier = max(r.DetTier, f.Tier)
	}
	r.Inputs = inputs
	return r
}

func botTypeOK(cfg *tiers.Config, got string) bool {
	want := cfg.Tiers[0].BotConditions.UpdateType
	return want == "" || got == want
}
