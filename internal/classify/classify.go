// Package classify asks a model for a second-opinion review tier and turns the outcome into an ai/v1 result.
package classify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/bearingsoftware/risk-gate/internal/decision"
	"github.com/bearingsoftware/risk-gate/prompts"
)

const (
	DiffCapBytes = 32000
	maxFiles     = 300
)

type File struct {
	Path, PreviousPath, Status string
	Additions, Deletions       int
}

// Input is everything the model sees about one pull request. All of it is untrusted except TierFile and Decision.
type Input struct {
	Title, Body string
	Files       []File
	Diff        string
	TierFile    []byte
	Decision    []byte
}

// Outcome is what one classification produced, before it is wrapped in a record.
type Outcome struct {
	SkipReason    string
	AI            *decision.AIResult
	ModelUsed     string
	Usage         *decision.AIUsage
	CostUSD       *float64
	LatencyMS     *int64
	DiffTruncated bool
}

func (o Outcome) Status() string {
	if o.AI != nil {
		return decision.AIClassified
	}
	return decision.AISkipped
}

// UserTurn renders the single user message; every piece sits in its own tag.
func UserTurn(in Input) (string, bool) {
	diff, truncated := CapDiff(StripInvisible(in.Diff), DiffCapBytes)
	if truncated {
		diff += fmt.Sprintf("[diff truncated at %d bytes]\n", DiffCapBytes)
	}
	var files strings.Builder
	for i, f := range in.Files {
		if i == maxFiles {
			fmt.Fprintf(&files, "[%d more files not listed]\n", len(in.Files)-maxFiles)
			break
		}
		prev := ""
		if f.PreviousPath != "" {
			prev = " (from " + f.PreviousPath + ")"
		}
		fmt.Fprintf(&files, "%s %s%s +%d -%d\n", f.Status, f.Path, prev, f.Additions, f.Deletions)
	}
	var sb strings.Builder
	section := func(tag, content string) {
		fmt.Fprintf(&sb, "<%s>\n%s\n</%s>\n", tag, Neutralise(StripInvisible(content)), tag)
	}
	section("tier_file", string(in.TierFile))
	section("deterministic_decision", string(in.Decision))
	section("pr_title", CleanProse(in.Title))
	section("pr_body", CleanProse(in.Body))
	section("files", files.String())
	section("diff", diff)
	return sb.String(), truncated
}

func creditMessage(e *APIError) bool {
	switch e.Status {
	case http.StatusBadRequest, http.StatusPaymentRequired, http.StatusForbidden, http.StatusTooManyRequests:
	default:
		return false
	}
	body := strings.ToLower(e.Body)
	for _, w := range []string{"credit", "billing", "spend limit", "usage limit"} {
		if strings.Contains(body, w) {
			return true
		}
	}
	return false
}

// Classify runs one call. A failure of any kind is a skip with a reason, never an error.
func Classify(ctx context.Context, api API, p prompts.Classifier, in Input, lg *log.Logger) Outcome {
	user, truncated := UserTurn(in)
	out := Outcome{DiffTruncated: truncated}

	start := time.Now()
	resp, err := api.Call(ctx, string(p.System), user, p.Schema)
	ms := time.Since(start).Milliseconds()
	if err != nil {
		var ae *APIError
		switch {
		case errors.As(err, &ae) && creditMessage(ae):
			out.SkipReason = decision.SkipCreditOrLimit
			lg.Printf("::warning::anthropic refused for credit or spend limit (status %d)", ae.Status)
		default:
			out.SkipReason = decision.SkipAPIError
			if ae != nil {
				lg.Printf("::warning::anthropic api error (status %d)", ae.Status)
			} else {
				lg.Printf("::warning::anthropic api error: %v", err)
			}
		}
		return out
	}
	out.LatencyMS = &ms
	out.ModelUsed = resp.Model
	u := resp.Usage
	out.Usage = &u
	out.CostUSD = Cost(resp.Model, u)

	switch resp.StopReason {
	case "refusal":
		out.SkipReason = decision.SkipRefusal
		return out
	case "max_tokens":
		out.SkipReason = decision.SkipTruncated
		return out
	}
	res, err := parse(p, resp.Text)
	if err != nil {
		lg.Printf("::warning::model output rejected (%d bytes): %v", len(resp.Text), err)
		out.SkipReason = decision.SkipParseError
		return out
	}
	out.AI = res
	return out
}

func parse(p prompts.Classifier, text string) (*decision.AIResult, error) {
	c := jsonschema.NewCompiler()
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(p.Schema))
	if err != nil {
		return nil, fmt.Errorf("classifier schema: %w", err)
	}
	if err := c.AddResource("classifier.json", doc); err != nil {
		return nil, fmt.Errorf("classifier schema: %w", err)
	}
	s, err := c.Compile("classifier.json")
	if err != nil {
		return nil, fmt.Errorf("classifier schema: %w", err)
	}
	v, err := jsonschema.UnmarshalJSON(strings.NewReader(text))
	if err != nil {
		return nil, fmt.Errorf("output is not json: %w", err)
	}
	if err := s.Validate(v); err != nil {
		return nil, fmt.Errorf("output fails schema: %w", err)
	}
	var r decision.AIResult
	if err := json.Unmarshal([]byte(text), &r); err != nil {
		return nil, fmt.Errorf("decoding output: %w", err)
	}
	for i, s := range r.Reasons {
		r.Reasons[i] = ModelString(s)
	}
	for i, s := range r.SecurityConcerns {
		r.SecurityConcerns[i] = ModelString(s)
	}
	if r.SecurityConcerns == nil {
		r.SecurityConcerns = []string{}
	}
	return &r, nil
}
