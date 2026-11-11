package run

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/bearingsoftware/risk-gate/internal/classify"
	"github.com/bearingsoftware/risk-gate/internal/decision"
	"github.com/bearingsoftware/risk-gate/internal/gh"
	"github.com/bearingsoftware/risk-gate/prompts"
)

const (
	aiCheckName    = "risk-gate-ai"
	aiPublisher    = "github-actions"
	aiPromptVer    = "v1"
	aiCheckTitle   = "AI benchmark recorded (shadow)"
	aiCheckSummary = "Shadow benchmark only. Not used by the gate."
)

// ClassifyGitHub is the slice of the GitHub API the classifier uses.
type ClassifyGitHub interface {
	PullRequest(ctx context.Context, n int) (gh.PullRequest, error)
	Files(ctx context.Context, n int) ([]gh.File, error)
	Diff(ctx context.Context, n int) (string, error)
	CheckRuns(ctx context.Context, sha, name string) ([]gh.CheckRunInfo, error)
	CreateCheckRun(ctx context.Context, cr gh.CheckRun) error
}

type ClassifyOptions struct {
	Repo         string
	PR           int
	DecisionPath string
	TiersPath    string
	Publish      bool
	RunID        int64
	RunAttempt   int
	Event        string
	EventAction  string
	// API is nil when there is no key.
	API classify.API
	Now func() time.Time
	Log *log.Logger
}

// Classify records one AI benchmark for the PR head. It returns nil when nothing was recorded:
// a fork, or a head that already has a classified record.
func Classify(ctx context.Context, o ClassifyOptions, g ClassifyGitHub) (*decision.AIRecord, error) {
	detData, err := os.ReadFile(o.DecisionPath)
	if err != nil {
		return nil, fmt.Errorf("reading decision: %w", err)
	}
	var det decision.Record
	if err := json.Unmarshal(detData, &det); err != nil {
		return nil, fmt.Errorf("parsing decision: %w", err)
	}
	if det.Schema != decision.Schema {
		return nil, fmt.Errorf("decision schema %q, want %q", det.Schema, decision.Schema)
	}
	tiersData, err := os.ReadFile(o.TiersPath)
	if err != nil {
		return nil, fmt.Errorf("reading tiers file: %w", err)
	}
	event, err := normaliseEvent(o.Event)
	if err != nil {
		return nil, err
	}
	prompt, err := prompts.LoadClassifier(aiPromptVer)
	if err != nil {
		return nil, err
	}
	pr, err := g.PullRequest(ctx, o.PR)
	if err != nil {
		return nil, fmt.Errorf("fetching pull request: %w", err)
	}

	rec := decision.AIRecord{
		Schema: decision.AISchema, Repo: o.Repo, PR: o.PR, BaseSHA: pr.BaseSHA, HeadSHA: pr.HeadSHA,
		Run:           decision.Run{ID: o.RunID, Attempt: max(o.RunAttempt, 1), Event: event, Action: o.EventAction, At: o.Now().UTC()},
		PromptVersion: prompt.Version, PromptSHA256: prompt.SHA256(), ModelRequested: classify.Model,
		DetTier: det.DetTier, WeightsVersion: det.Scorer.WeightsVer, Lane: det.Lane,
	}
	skip := func(reason string) { rec.Status, rec.SkipReason = decision.AISkipped, &reason }

	if pr.Fork {
		skip(decision.SkipFork)
		o.Log.Printf("::notice::fork pull request: token is read-only, no AI record published")
		return &rec, nil
	}
	if classified(ctx, g, o, pr.HeadSHA) {
		o.Log.Printf("::notice::already classified")
		return nil, nil
	}

	switch {
	case det.Lane != "human":
		skip(decision.SkipNotHumanLane)
	case o.API == nil:
		skip(decision.SkipNoKey)
		o.Log.Printf("::notice::no Anthropic key, AI classification skipped")
	default:
		in, err := classifyInput(ctx, g, o, pr, tiersData, detData)
		if err != nil {
			return nil, err
		}
		out := classify.Classify(ctx, o.API, prompt, in, o.Log)
		rec.AI, rec.Usage, rec.CostUSD, rec.LatencyMS, rec.DiffTruncated = out.AI, out.Usage, out.CostUSD, out.LatencyMS, out.DiffTruncated
		if out.ModelUsed != "" {
			rec.ModelUsed = &out.ModelUsed
		}
		rec.Status = out.Status()
		if out.SkipReason != "" {
			skip(out.SkipReason)
		}
	}
	return &rec, publishAI(ctx, g, o, rec)
}

func classified(ctx context.Context, g ClassifyGitHub, o ClassifyOptions, sha string) bool {
	runs, err := g.CheckRuns(ctx, sha, aiCheckName)
	if err != nil {
		o.Log.Printf("::warning::existing AI check runs unavailable, continuing: %v", err)
		return false
	}
	for _, r := range runs {
		if r.AppSlug != aiPublisher {
			continue
		}
		if prev, ok := decision.ParseAICheckText(r.Text); ok && prev.Status == decision.AIClassified && prev.HeadSHA == sha {
			return true
		}
	}
	return false
}

func classifyInput(ctx context.Context, g ClassifyGitHub, o ClassifyOptions, pr gh.PullRequest, tiersData, detData []byte) (classify.Input, error) {
	files, err := g.Files(ctx, o.PR)
	if err != nil {
		return classify.Input{}, fmt.Errorf("fetching files: %w", err)
	}
	diff, err := g.Diff(ctx, o.PR)
	if err != nil {
		o.Log.Printf("::warning::diff unavailable, classifying without it: %v", err)
		diff = ""
	}
	in := classify.Input{Title: pr.Title, Body: pr.Body, Diff: diff, TierFile: tiersData, Decision: slimDecision(detData)}
	for _, f := range files {
		in.Files = append(in.Files, classify.File(f))
	}
	return in, nil
}

// slimDecision drops inputs.files, which the model already gets as its own section.
func slimDecision(b []byte) []byte {
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return b
	}
	if in, ok := m["inputs"].(map[string]any); ok {
		delete(in, "files")
	}
	out, err := json.Marshal(m)
	if err != nil {
		return b
	}
	return out
}

func publishAI(ctx context.Context, g ClassifyGitHub, o ClassifyOptions, rec decision.AIRecord) error {
	if !o.Publish {
		return nil
	}
	b, err := rec.Marshal()
	if err != nil {
		return err
	}
	err = g.CreateCheckRun(ctx, gh.CheckRun{
		Name: aiCheckName, HeadSHA: rec.HeadSHA, Conclusion: "neutral",
		Title: aiCheckTitle, Summary: aiCheckSummary, Text: decision.AICheckText(b),
	})
	var se *gh.StatusError
	if errors.As(err, &se) && se.Status == http.StatusForbidden {
		o.Log.Printf("::warning::read-only token, AI check run skipped: %v", err)
		return nil
	}
	if err != nil {
		return fmt.Errorf("publishing AI check run: %w", err)
	}
	return nil
}
