// Package run wires the GitHub inputs, optional tool outputs and the scorer into one decision.
package run

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/bearingsoftware/risk-gate/internal/decision"
	"github.com/bearingsoftware/risk-gate/internal/gh"
	"github.com/bearingsoftware/risk-gate/internal/sarif"
	"github.com/bearingsoftware/risk-gate/internal/score"
	"github.com/bearingsoftware/risk-gate/internal/tiers"
	"github.com/bearingsoftware/risk-gate/weights"
)

const ScorerVersion = "0.1.0"

// GitHub is the slice of the GitHub API the scorer uses.
type GitHub interface {
	PullRequest(ctx context.Context, n int) (gh.PullRequest, error)
	Files(ctx context.Context, n int) ([]gh.File, error)
	AuthorCommits(ctx context.Context, login, branch string, since time.Time, max int) (int, error)
	RecentMergedPRs(ctx context.Context, branch string, since time.Time, maxPRs, exclude int) (gh.MergedPRs, error)
	LatestLabelEvent(ctx context.Context, n int, label string) (gh.LabelEvent, bool, error)
	RemoveLabel(ctx context.Context, n int, label string) error
	AddLabel(ctx context.Context, n int, label string) error
	HeadCommitDate(ctx context.Context, sha string) (time.Time, error)
	LatestForcePush(ctx context.Context, n int) (time.Time, bool, error)
	CreateCheckRun(ctx context.Context, cr gh.CheckRun) error
}

type Options struct {
	Repo         string
	PR           int
	TiersPath    string
	WeightsPath  string
	WeightsVer   int
	Mode         string
	SarifBase    string
	SarifHead    string
	CoverageBase string
	CoverageHead string
	APIBreak     *bool
	RunID        int64
	RunAttempt   int
	Event        string
	EventAction  string
	Publish      bool
	Label        bool
	Now          func() time.Time
	Log          *log.Logger
}

const (
	checkName               = "risk-gate"
	defaultAttestationLabel = "tier2-self-reviewed"
)

func Run(ctx context.Context, o Options, g GitHub) (decision.Record, error) {
	tiersData, err := os.ReadFile(o.TiersPath)
	if err != nil {
		return decision.Record{}, fmt.Errorf("reading tiers file: %w", err)
	}
	cfg, err := tiers.Parse(tiersData)
	if err != nil {
		return decision.Record{}, err
	}
	weightsData, err := loadWeights(o)
	if err != nil {
		return decision.Record{}, err
	}
	w, err := score.ParseWeights(weightsData)
	if err != nil {
		return decision.Record{}, err
	}
	if o.Mode != "shadow" && o.Mode != "enforce" {
		return decision.Record{}, fmt.Errorf("mode must be shadow or enforce, got %q", o.Mode)
	}
	if o.Mode == "enforce" && cfg.Attestation == nil {
		return decision.Record{}, fmt.Errorf("enforce mode needs an attestation block in the tiers file")
	}
	event, err := normaliseEvent(o.Event)
	if err != nil {
		return decision.Record{}, err
	}

	pr, err := g.PullRequest(ctx, o.PR)
	if err != nil {
		return decision.Record{}, fmt.Errorf("fetching pull request: %w", err)
	}
	files, err := g.Files(ctx, o.PR)
	if err != nil {
		return decision.Record{}, fmt.Errorf("fetching files: %w", err)
	}

	in := score.Input{
		Title: pr.Title, Body: pr.Body, Labels: pr.Labels, Author: pr.Author, AuthorBot: pr.AuthorBot,
		HeadRef: pr.HeadRef, FileListIncomplete: pr.ChangedFiles > len(files), Tiers: cfg, Weights: w, APIBreak: o.APIBreak,
	}
	for _, f := range files {
		in.Files = append(in.Files, score.File(f))
	}
	if err := optionalSignals(o, &in); err != nil {
		return decision.Record{}, err
	}
	now := o.Now()
	if !pr.AuthorBot {
		authorTenure(ctx, g, o, pr, now, &in)
	}
	hotFiles(ctx, g, o, pr, now, w, &in)

	res := score.Evaluate(in)

	tiersSum := sha256.Sum256(tiersData)
	rec := decision.Record{
		Schema: decision.Schema, Repo: o.Repo, PR: o.PR,
		BaseRef: pr.BaseRef, BaseSHA: pr.BaseSHA, HeadRef: pr.HeadRef, HeadSHA: pr.HeadSHA,
		Run:  decision.Run{ID: o.RunID, Attempt: max(o.RunAttempt, 1), Event: event, Action: o.EventAction, At: now.UTC()},
		Mode: o.Mode,
		Scorer: decision.Scorer{
			Version: ScorerVersion, WeightsVer: w.Version, WeightsSHA256: w.SHA256,
		},
		TiersFileSHA256: hex.EncodeToString(tiersSum[:]),
		Lane:            res.Lane,
		Author:          decision.Author{Login: pr.Author, Bot: pr.AuthorBot},
		PathTier:        res.PathTier, Floors: res.Floors, Score: res.Score, ScoreItems: res.ScoreItems,
		DetTier: res.DetTier, Inputs: res.Inputs, Unavailable: res.Unavailable,
	}
	if err := attest(ctx, g, o, cfg, pr, &rec); err != nil {
		return decision.Record{}, err
	}

	b, err := rec.Marshal()
	if err != nil {
		return decision.Record{}, err
	}
	if (o.Publish || o.Label) && pr.Fork {
		o.Log.Printf("::notice::fork pull request: token is read-only, skipping check run and label")
		return rec, nil
	}
	if o.Publish {
		err := g.CreateCheckRun(ctx, gh.CheckRun{
			Name: checkName, HeadSHA: pr.HeadSHA, Conclusion: rec.Check.Conclusion,
			Title:   fmt.Sprintf("risk-gate: tier %d", rec.DetTier),
			Summary: Summary(rec, o.Mode),
			Text:    decision.CheckText(b),
		})
		var se *gh.StatusError
		if err != nil && o.Mode == "shadow" && errors.As(err, &se) && se.Status == http.StatusForbidden {
			o.Log.Printf("::warning::read-only token, check run and label skipped: %v", err)
			return rec, nil
		}
		if err != nil {
			return decision.Record{}, fmt.Errorf("publishing check run: %w", err)
		}
	}
	if o.Label {
		if err := setTierLabel(ctx, g, o.PR, pr.Labels, rec.DetTier); err != nil {
			o.Log.Printf("tier label not applied: %v", err)
		}
	}
	return rec, nil
}

const tierLabelPrefix = "risk:tier-"

func setTierLabel(ctx context.Context, g GitHub, n int, current []string, tier int) error {
	want := fmt.Sprintf("%s%d", tierLabelPrefix, tier)
	for _, l := range current {
		if strings.HasPrefix(l, tierLabelPrefix) && l != want {
			if err := g.RemoveLabel(ctx, n, l); err != nil {
				return fmt.Errorf("removing %s: %w", l, err)
			}
		}
	}
	if err := g.AddLabel(ctx, n, want); err != nil {
		return fmt.Errorf("adding %s: %w", want, err)
	}
	return nil
}

func loadWeights(o Options) ([]byte, error) {
	if o.WeightsPath != "" {
		b, err := os.ReadFile(o.WeightsPath)
		if err != nil {
			return nil, fmt.Errorf("reading weights: %w", err)
		}
		return b, nil
	}
	return weights.Load(max(o.WeightsVer, 1))
}

func normaliseEvent(e string) (string, error) {
	switch e {
	case "pull_request", "pull_request_target":
		return "pull_request", nil
	case "pull_request_review", "workflow_dispatch":
		return e, nil
	case "":
		return "workflow_dispatch", nil
	}
	return "", fmt.Errorf("unsupported event %q", e)
}

func optionalSignals(o Options, in *score.Input) error {
	if o.SarifBase != "" && o.SarifHead != "" {
		base, err := sarif.ParseFile(o.SarifBase)
		if err != nil {
			return err
		}
		head, err := sarif.ParseFile(o.SarifHead)
		if err != nil {
			return err
		}
		n := sarif.NewHigh(base, head)
		in.SastNewHigh = &n
	}
	if o.CoverageBase != "" && o.CoverageHead != "" {
		base, err := readPercent(o.CoverageBase)
		if err != nil {
			return err
		}
		head, err := readPercent(o.CoverageHead)
		if err != nil {
			return err
		}
		d := head - base
		in.CoverageDelta = &d
	}
	return nil
}

func readPercent(p string) (float64, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return 0, fmt.Errorf("reading coverage: %w", err)
	}
	f, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(string(b)), "%"), 64)
	if err != nil {
		return 0, fmt.Errorf("coverage file %s: %w", p, err)
	}
	return f, nil
}

func authorTenure(ctx context.Context, g GitHub, o Options, pr gh.PullRequest, now time.Time, in *score.Input) {
	n, err := g.AuthorCommits(ctx, pr.Author, pr.BaseRef, now.AddDate(0, 0, -180), 1000)
	if err != nil {
		o.Log.Printf("author commits unavailable: %v", err)
		return
	}
	first := false
	if n == 0 {
		ever, err := g.AuthorCommits(ctx, pr.Author, pr.BaseRef, time.Time{}, 1)
		if err != nil {
			o.Log.Printf("author history unavailable: %v", err)
			in.AuthorCommits180d = &n
			return
		}
		first = ever == 0
	}
	in.AuthorCommits180d = &n
	in.FirstTimeContributor = &first
}

func hotFiles(ctx context.Context, g GitHub, o Options, pr gh.PullRequest, now time.Time, w *score.Weights, in *score.Input) {
	lb := w.HotLookback
	merged, err := g.RecentMergedPRs(ctx, pr.BaseRef, now.AddDate(0, 0, -lb.Days), lb.MaxPRs, pr.Number)
	if err != nil {
		o.Log.Printf("hot files unavailable: %v", err)
		return
	}
	o.Log.Printf("hot files: examined %d merged PRs in %d days (lookback capped at %d, hit cap: %v)", merged.Examined, lb.Days, lb.MaxPRs, merged.Capped)
	touched := map[string]int{}
	for _, fs := range merged.Files {
		for _, f := range slices.Compact(slices.Sorted(slices.Values(fs))) {
			touched[f]++
		}
	}
	n := 0
	for _, f := range in.Files {
		if touched[f.Path] > lb.TouchedByMoreThan {
			n++
		}
	}
	in.HotFiles = &n
}

func attest(ctx context.Context, g GitHub, o Options, cfg *tiers.Config, pr gh.PullRequest, rec *decision.Record) error {
	rec.Attestation = decision.Attestation{Label: attestationLabel(cfg)}
	rec.Check = decision.Check{Conclusion: "success"}
	a := cfg.Attestation
	if a == nil {
		return nil
	}
	rec.Attestation.Required = rec.DetTier == 2
	present := slices.Contains(pr.Labels, a.Label)
	removeFailed := false
	if present && o.EventAction == "synchronize" {
		if o.Mode == "enforce" {
			if err := g.RemoveLabel(ctx, o.PR, a.Label); err != nil {
				o.Log.Printf("removing stale attestation label: %v", err)
				removeFailed = true
			}
		}
		present = false
	}
	if present {
		ev, found, err := g.LatestLabelEvent(ctx, o.PR, a.Label)
		if err != nil {
			return fmt.Errorf("reading label timeline: %w", err)
		}
		if found {
			by, at := ev.Actor, ev.At.UTC()
			rec.Attestation.By, rec.Attestation.At = &by, &at
			rec.Attestation.Attested = slices.Contains(a.Permitted, ev.Actor) && freshAfterPush(ctx, g, o, pr, ev.At)
		}
	}
	if rec.Attestation.Required {
		rec.Check.WouldBlock = !rec.Attestation.Attested
		if rec.Attestation.Attested {
			rec.Check.Reason = "tier 2 attested after latest push"
		} else {
			rec.Check.Reason = fmt.Sprintf("tier 2 needs label %q applied by a permitted user after the latest push", a.Label)
		}
	}
	if removeFailed && rec.Attestation.Required {
		rec.Check.Reason = "could not remove the stale attestation label"
	}
	if o.Mode == "enforce" && rec.Check.WouldBlock {
		rec.Check.Conclusion = "failure"
	}
	return nil
}

// freshAfterPush reports whether the label was applied after the head commit and any force-push.
// A lookup that fails counts as not fresh.
func freshAfterPush(ctx context.Context, g GitHub, o Options, pr gh.PullRequest, at time.Time) bool {
	commit, err := g.HeadCommitDate(ctx, pr.HeadSHA)
	if err != nil {
		o.Log.Printf("head commit date unavailable: %v", err)
		return false
	}
	if !at.After(commit) {
		return false
	}
	forced, found, err := g.LatestForcePush(ctx, o.PR)
	if err != nil {
		o.Log.Printf("force-push lookup unavailable: %v", err)
		return false
	}
	return !found || at.After(forced)
}

func attestationLabel(cfg *tiers.Config) string {
	if cfg.Attestation == nil {
		return defaultAttestationLabel
	}
	return cfg.Attestation.Label
}

// Summary is the human-readable check summary.
func Summary(r decision.Record, mode string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "| | |\n|---|---|\n| Tier | %d |\n| Path tier | %d |\n| Score | %d |\n| Lane | %s |\n| Mode | %s |\n", r.DetTier, r.PathTier, r.Score, r.Lane, mode)
	if len(r.Floors) > 0 {
		var fl []string
		for _, f := range r.Floors {
			fl = append(fl, fmt.Sprintf("%s (%d)", f.Rule, f.Tier))
		}
		fmt.Fprintf(&sb, "| Floors | %s |\n", strings.Join(fl, ", "))
	}
	if len(r.ScoreItems) > 0 {
		var it []string
		for _, i := range r.ScoreItems {
			it = append(it, fmt.Sprintf("%s +%d", i.Signal, i.Points))
		}
		fmt.Fprintf(&sb, "| Signals | %s |\n", strings.Join(it, ", "))
	}
	if len(r.Unavailable) > 0 {
		fmt.Fprintf(&sb, "| Unavailable | %s |\n", strings.Join(r.Unavailable, ", "))
	}
	if r.Check.Reason != "" {
		fmt.Fprintf(&sb, "| Check | %s |\n", r.Check.Reason)
	}
	return sb.String()
}
