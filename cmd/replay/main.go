// Command replay scores every merged PR in the frozen baseline window under each weights file and
// writes a tier-distribution report.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/bearingsoftware/risk-gate/internal/score"
	"github.com/bearingsoftware/risk-gate/internal/tiers"
)

type repoSpec struct {
	Name     string
	Branch   string
	TiersRef string
}

var repoSpecs = []repoSpec{
	{"secure-repo", "develop", "develop"},
	{"joule-genie", "develop", "chore/finch-523-review-tiers"},
	{"component-test-framework", "main", "main"},
}

type namedWeights struct {
	Name string
	W    *score.Weights
}

type baselineFile struct {
	Window struct {
		MergedFrom string `json:"merged_from"`
		MergedTo   string `json:"merged_to"`
	} `json:"window"`
	Repos map[string]struct {
		MergedTotal int `json:"merged_total"`
		OtherBase   int `json:"other_base"`
		ReleaseLane int `json:"release_lane"`
		Human       struct {
			Count int `json:"count"`
		} `json:"human"`
		Bot struct {
			Count int `json:"count"`
		} `json:"bot"`
	} `json:"repos"`
}

type window struct {
	from, end time.Time
}

type prRow struct {
	Repo       string   `json:"repo"`
	Base       string   `json:"base"`
	Weights    string   `json:"weights"`
	PR         int      `json:"pr"`
	Lane       string   `json:"lane"`
	DetTier    int      `json:"det_tier"`
	Score      int      `json:"score"`
	PathTier   int      `json:"path_tier"`
	Floors     []string `json:"floors"`
	ScoreItems []string `json:"score_items"`
}

type baselineCheck struct {
	ReplayTotal     int `json:"replay_total"`
	BaselineTotal   int `json:"baseline_total_minus_other_base"`
	ReplayHuman     int `json:"replay_human"`
	BaselineHuman   int `json:"baseline_human"`
	ReplayBot       int `json:"replay_bot"`
	BaselineBot     int `json:"baseline_bot"`
	ReplayRelease   int `json:"replay_release"`
	BaselineRelease int `json:"baseline_release"`
}

type repoOut struct {
	IntegrationBranch string                             `json:"integration_branch"`
	TiersRef          string                             `json:"tiers_ref"`
	Weights           map[string]map[string]*laneSummary `json:"weights"`
	BaselineCheck     baselineCheck                      `json:"baseline_check"`
}

type output struct {
	GeneratedAt   string             `json:"generated_at"`
	WindowFrom    string             `json:"window_from"`
	WindowTo      string             `json:"window_to"`
	WeightsSHA256 map[string]string  `json:"weights_sha256"`
	APICalls      int64              `json:"api_calls"`
	Notes         []string           `json:"notes"`
	Repos         map[string]repoOut `json:"repos"`
	Rows          []prRow            `json:"rows"`
}

var notes = []string{
	"sast_new_high, coverage_delta and api_break are nil (unavailable): their floors and score items never fire",
	"first_time_contributor is 'no commits by author in the cached integration-branch history before created_at', not all-time",
	"commit history is cached from the earliest created_at of the window minus 180d, so tenure covers the full lookback",
	"hot files look back over cached PRs merged from window start minus 30d; the live 100-PR cap is applied to the most recent matches; touched_by_more_than comes from each weights file",
	"author_commits uses committer date on the integration branch; live uses the API author filter with the since parameter",
	"joule-genie tier file is read from chore/finch-523-review-tiers (unmerged), as specified",
	"release lane is PRs headed by release.heads that merged into main; hot files and tenure use each base's own history, as live does",
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "replay:", err)
		os.Exit(1)
	}
}

// parseWeightsFlag reads name=path pairs, e.g. v1=weights/v1.yml,v2=weights/v2.yml.
func parseWeightsFlag(s string) ([]namedWeights, error) {
	var out []namedWeights
	for _, pair := range strings.Split(s, ",") {
		name, path, ok := strings.Cut(pair, "=")
		if !ok || name == "" {
			return nil, fmt.Errorf("weights %q: want name=path", pair)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading weights %s: %w", name, err)
		}
		w, err := score.ParseWeights(b)
		if err != nil {
			return nil, fmt.Errorf("weights %s: %w", name, err)
		}
		out = append(out, namedWeights{Name: name, W: w})
	}
	return out, nil
}

func run(args []string, stdout io.Writer) error {
	fl := flag.NewFlagSet("replay", flag.ContinueOnError)
	cacheDir := fl.String("cache", "", "directory for cached GitHub responses (required)")
	outPath := fl.String("out", "", "output JSON path (required)")
	weightsFlag := fl.String("weights", "v1=weights/v1.yml,v2=weights/v2.yml", "name=path weights files to replay; the first is the baseline check")
	baselinePath := fl.String("baseline", "baseline/2026-11-10.json", "frozen baseline file")
	owner := fl.String("owner", "bearingsoftware", "GitHub owner of the trial repos")
	workers := fl.Int("workers", 4, "concurrent gh calls")
	if err := fl.Parse(args); err != nil {
		return err
	}
	if *cacheDir == "" || *outPath == "" {
		return errors.New("--cache and --out are required")
	}

	ws, err := parseWeightsFlag(*weightsFlag)
	if err != nil {
		return err
	}
	bRaw, err := os.ReadFile(*baselinePath)
	if err != nil {
		return fmt.Errorf("reading baseline: %w", err)
	}
	var bl baselineFile
	if err := json.Unmarshal(bRaw, &bl); err != nil {
		return fmt.Errorf("decoding baseline: %w", err)
	}
	from, err := time.Parse("2006-01-02", bl.Window.MergedFrom)
	if err != nil {
		return fmt.Errorf("baseline merged_from: %w", err)
	}
	to, err := time.Parse("2006-01-02", bl.Window.MergedTo)
	if err != nil {
		return fmt.Errorf("baseline merged_to: %w", err)
	}
	win := window{from: from, end: to.AddDate(0, 0, 1)}

	c := &client{owner: *owner, cache: *cacheDir}
	out := output{
		GeneratedAt:   time.Now().UTC().Format(time.RFC3339),
		WindowFrom:    bl.Window.MergedFrom,
		WindowTo:      bl.Window.MergedTo,
		WeightsSHA256: map[string]string{},
		Notes:         notes,
		Repos:         map[string]repoOut{},
	}
	for _, nw := range ws {
		out.WeightsSHA256[nw.Name] = nw.W.SHA256
	}
	for _, spec := range repoSpecs {
		ro, rows, err := runRepo(c, spec, ws, win, *workers)
		if err != nil {
			return fmt.Errorf("%s: %w", spec.Name, err)
		}
		b := bl.Repos[spec.Name]
		v1 := ro.Weights[ws[0].Name]
		ro.BaselineCheck = baselineCheck{
			ReplayTotal:     rows.total,
			BaselineTotal:   b.MergedTotal - b.OtherBase,
			ReplayHuman:     laneCount(v1, "human"),
			BaselineHuman:   b.Human.Count,
			ReplayBot:       laneCount(v1, "bot"),
			BaselineBot:     b.Bot.Count,
			ReplayRelease:   laneCount(v1, "release"),
			BaselineRelease: b.ReleaseLane,
		}
		out.Repos[spec.Name] = ro
		out.Rows = append(out.Rows, rows.rows...)
		for _, nw := range ws {
			fmt.Fprintf(stdout, "%s %s: human tier2 %.1f%% of %d\n", spec.Name, nw.Name,
				humanTier2Pct(ro.Weights[nw.Name]), laneCount(ro.Weights[nw.Name], "human"))
		}
	}
	out.APICalls = c.calls.Load()

	buf, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding output: %w", err)
	}
	if err := os.WriteFile(*outPath, append(buf, '\n'), 0o644); err != nil {
		return fmt.Errorf("writing output: %w", err)
	}
	fmt.Fprintf(stdout, "api calls this run: %d; wrote %s\n", out.APICalls, *outPath)
	return nil
}

func humanTier2Pct(lanes map[string]*laneSummary) float64 {
	if s := lanes["human"]; s != nil {
		return s.Tier2Pct
	}
	return 0
}

func laneCount(lanes map[string]*laneSummary, lane string) int {
	if s := lanes[lane]; s != nil {
		return s.Count
	}
	return 0
}

type repoRows struct {
	total int
	all   []scoredPR
	rows  []prRow
}

// runRepo scores the integration branch's merged PRs, plus the release lane (PRs headed by a release
// head that merged into another base, normally main). Hot files and tenure use each base's own history.
func runRepo(c *client, spec repoSpec, ws []namedWeights, win window, workers int) (repoOut, repoRows, error) {
	var ro repoOut
	var rr repoRows
	ro.IntegrationBranch = spec.Branch
	ro.TiersRef = spec.TiersRef

	raw, err := c.tiersFile(spec.Name, spec.TiersRef)
	if err != nil {
		return ro, rr, err
	}
	cfg, err := tiers.Parse(raw)
	if err != nil {
		return ro, rr, err
	}

	bases := []string{spec.Branch}
	if spec.Branch != "main" {
		bases = append(bases, "main")
	}
	for _, b := range bases {
		scored, rows, err := runBase(c, spec, cfg, b, ws, win, workers)
		if err != nil {
			return ro, rr, fmt.Errorf("base %s: %w", b, err)
		}
		rr.all = append(rr.all, scored...)
		rr.rows = append(rr.rows, rows...)
	}
	rr.total = len(rr.all)
	if rr.total == 0 {
		return ro, rr, errors.New("no merged PRs in window")
	}

	slices.SortFunc(rr.rows, func(a, b prRow) int {
		if a.PR != b.PR {
			return a.PR - b.PR
		}
		return strings.Compare(a.Weights, b.Weights)
	})
	ro.Weights = map[string]map[string]*laneSummary{}
	for _, nw := range ws {
		ro.Weights[nw.Name] = summarise(rr.all, nw.Name)
	}
	return ro, rr, nil
}

func runBase(c *client, spec repoSpec, cfg *tiers.Config, base string, ws []namedWeights, win window, workers int) ([]scoredPR, []prRow, error) {
	cutoff := win.from.AddDate(0, 0, -30)
	list, err := c.mergedList(spec.Name, base, cutoff)
	if err != nil {
		return nil, nil, err
	}
	var prior, inWin []prRec
	for _, p := range list {
		if p.MergedAt == nil || p.MergedAt.Before(cutoff) || !p.MergedAt.Before(win.end) {
			continue
		}
		prior = append(prior, p)
		if !p.MergedAt.Before(win.from) && (base == spec.Branch || cfg.IsReleaseHead(p.Head.Ref)) {
			inWin = append(inWin, p)
		}
	}
	if len(inWin) == 0 {
		return nil, nil, nil
	}

	if err := forEach(prior, workers, func(p prRec) error {
		_, err := c.files(spec.Name, p.Number)
		return err
	}); err != nil {
		return nil, nil, err
	}
	if err := forEach(inWin, workers, func(p prRec) error {
		_, err := c.detail(spec.Name, p.Number)
		return err
	}); err != nil {
		return nil, nil, err
	}

	earliest := inWin[0].CreatedAt
	for _, p := range inWin {
		if p.CreatedAt.Before(earliest) {
			earliest = p.CreatedAt
		}
	}
	commits, err := c.commits(spec.Name, base, earliest.AddDate(0, 0, -tenureDays))
	if err != nil {
		return nil, nil, err
	}

	mergedPRs := make([]mergedPR, 0, len(prior))
	filesOf := map[int][]fileRec{}
	for _, p := range prior {
		fs, err := c.files(spec.Name, p.Number)
		if err != nil {
			return nil, nil, err
		}
		filesOf[p.Number] = fs
		names := make([]string, 0, len(fs))
		for _, f := range fs {
			names = append(names, f.Filename)
		}
		mergedPRs = append(mergedPRs, mergedPR{Number: p.Number, MergedAt: *p.MergedAt, Files: names})
	}

	var scored []scoredPR
	var rows []prRow
	for _, p := range inWin {
		det, err := c.detail(spec.Name, p.Number)
		if err != nil {
			return nil, nil, err
		}
		fs := filesOf[p.Number]
		in := score.Input{
			Title:              p.Title,
			Body:               p.Body,
			Author:             p.User.Login,
			AuthorBot:          p.User.Type == "Bot" || strings.HasSuffix(p.User.Login, "[bot]"),
			HeadRef:            p.Head.Ref,
			FileListIncomplete: det.ChangedFiles > len(fs),
			Tiers:              cfg,
		}
		for _, l := range p.Labels {
			in.Labels = append(in.Labels, l.Name)
		}
		names := make([]string, 0, len(fs))
		for _, f := range fs {
			in.Files = append(in.Files, score.File{Path: f.Filename, PreviousPath: f.PreviousFilename, Status: f.Status, Additions: f.Additions, Deletions: f.Deletions})
			names = append(names, f.Filename)
		}
		if !in.AuthorBot {
			tenure, ever := authorHistory(commits, p.User.Login, p.CreatedAt)
			first := ever == 0
			in.AuthorCommits180d = &tenure
			in.FirstTimeContributor = &first
		}

		results := map[string]score.Result{}
		for _, nw := range ws {
			x := in
			x.Weights = nw.W
			hot := hotFileCount(mergedPRs, p.Number, p.CreatedAt, names, nw.W.HotLookback)
			x.HotFiles = &hot
			results[nw.Name] = score.Evaluate(x)
		}
		scored = append(scored, scoredPR{Number: p.Number, Lane: results[ws[0].Name].Lane, Results: results})
		for _, nw := range ws {
			r := results[nw.Name]
			row := prRow{Repo: spec.Name, Base: base, Weights: nw.Name, PR: p.Number, Lane: r.Lane, DetTier: r.DetTier, Score: r.Score, PathTier: r.PathTier, Floors: []string{}, ScoreItems: []string{}}
			for _, f := range r.Floors {
				row.Floors = append(row.Floors, f.Rule)
			}
			for _, it := range r.ScoreItems {
				row.ScoreItems = append(row.ScoreItems, fmt.Sprintf("%s:%+d", it.Signal, it.Points))
			}
			rows = append(rows, row)
		}
	}
	return scored, rows, nil
}
