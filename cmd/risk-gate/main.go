package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/bearingsoftware/risk-gate/internal/gh"
	"github.com/bearingsoftware/risk-gate/internal/run"
	"github.com/bearingsoftware/risk-gate/internal/tiers"
)

func main() {
	os.Exit(execute(os.Args[1:], os.Stdout, os.Stderr, os.Getenv))
}

func execute(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: risk-gate score|lint-tiers [flags]")
		return 2
	}
	var err error
	switch args[0] {
	case "score":
		err = score(args[1:], stdout, stderr, getenv)
	case "lint-tiers":
		err = lintTiers(args[1:], stdout)
	default:
		fmt.Fprintf(stderr, "unknown command %q\n", args[0])
		return 2
	}
	if err != nil {
		fmt.Fprintln(stderr, "risk-gate:", err)
		return 1
	}
	return 0
}

func lintTiers(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("lint-tiers", flag.ContinueOnError)
	tiersPath := fs.String("tiers", ".github/review-tiers.yml", "tiers file")
	codeowners := fs.String("codeowners", ".github/CODEOWNERS", "CODEOWNERS file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	data, err := os.ReadFile(*tiersPath)
	if err != nil {
		return fmt.Errorf("reading tiers file: %w", err)
	}
	cfg, err := tiers.Parse(data)
	if err != nil {
		return err
	}
	f, err := os.Open(*codeowners)
	if err != nil {
		return fmt.Errorf("opening CODEOWNERS: %w", err)
	}
	defer f.Close()
	missing, err := cfg.MissingFromCodeowners(f)
	if err != nil {
		return err
	}
	for _, g := range missing {
		fmt.Fprintf(stdout, "tier 2 glob %q has no identical CODEOWNERS pattern\n", g)
	}
	if len(missing) > 0 {
		return fmt.Errorf("%d tier 2 glob(s) missing from CODEOWNERS", len(missing))
	}
	return nil
}

func score(args []string, stdout, stderr io.Writer, getenv func(string) string) error {
	fs := flag.NewFlagSet("score", flag.ContinueOnError)
	var o run.Options
	fs.StringVar(&o.Repo, "repo", "", "owner/name")
	fs.IntVar(&o.PR, "pr", 0, "pull request number")
	fs.StringVar(&o.TiersPath, "tiers", ".github/review-tiers.yml", "tiers file")
	fs.StringVar(&o.WeightsPath, "weights", "", "weights file; wins over --weights-version")
	fs.IntVar(&o.WeightsVer, "weights-version", 1, "embedded weights version N (weights/vN.yml)")
	fs.StringVar(&o.Mode, "mode", "shadow", "shadow or enforce")
	fs.StringVar(&o.SarifBase, "sarif-base", "", "SARIF from the base branch")
	fs.StringVar(&o.SarifHead, "sarif-head", "", "SARIF from the PR head")
	fs.StringVar(&o.CoverageBase, "coverage-base", "", "file holding the base coverage percent")
	fs.StringVar(&o.CoverageHead, "coverage-head", "", "file holding the head coverage percent")
	apiBreak := fs.String("api-break", "", "true or false when an API-break check ran")
	out := fs.String("out", "", "write the decision JSON here")
	fs.BoolVar(&o.Publish, "publish", false, "create the risk-gate check run")
	fs.BoolVar(&o.Label, "label", false, "set the single risk:tier-N label on the PR")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if o.Repo == "" || o.PR < 1 {
		return fmt.Errorf("--repo and --pr are required")
	}
	if (o.SarifBase == "") != (o.SarifHead == "") {
		return fmt.Errorf("--sarif-base and --sarif-head must be given together")
	}
	if (o.CoverageBase == "") != (o.CoverageHead == "") {
		return fmt.Errorf("--coverage-base and --coverage-head must be given together")
	}
	if *apiBreak != "" {
		b, err := strconv.ParseBool(*apiBreak)
		if err != nil {
			return fmt.Errorf("--api-break: %w", err)
		}
		o.APIBreak = &b
	}
	token := getenv("GITHUB_TOKEN")
	if token == "" {
		return fmt.Errorf("GITHUB_TOKEN is not set")
	}
	o.RunID, _ = strconv.ParseInt(getenv("GITHUB_RUN_ID"), 10, 64)
	o.RunAttempt, _ = strconv.Atoi(getenv("GITHUB_RUN_ATTEMPT"))
	o.Event = getenv("GITHUB_EVENT_NAME")
	if p := getenv("GITHUB_EVENT_PATH"); p != "" {
		action, err := eventAction(p)
		if err != nil {
			return err
		}
		o.EventAction = action
	}
	o.Now = time.Now
	o.Log = log.New(stderr, "", 0)

	rec, err := run.Run(context.Background(), o, gh.New(o.Repo, token))
	if err != nil {
		return err
	}
	b, err := rec.Marshal()
	if err != nil {
		return err
	}
	if *out != "" {
		if err := os.WriteFile(*out, append(b, '\n'), 0o644); err != nil {
			return fmt.Errorf("writing decision: %w", err)
		}
	}
	fmt.Fprintf(stdout, "tier %d (path %d, score %d, lane %s, conclusion %s)\n", rec.DetTier, rec.PathTier, rec.Score, rec.Lane, rec.Check.Conclusion)
	return nil
}

func eventAction(p string) (string, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return "", fmt.Errorf("reading event payload: %w", err)
	}
	var ev struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal(b, &ev); err != nil {
		return "", fmt.Errorf("parsing event payload: %w", err)
	}
	return ev.Action, nil
}
