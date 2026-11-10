// Package ghfake is an in-memory run.GitHub for tests.
package ghfake

import (
	"context"
	"time"

	"github.com/bearingsoftware/risk-gate/internal/gh"
)

type Fake struct {
	PR           gh.PullRequest
	FileList     []gh.File
	CommitsSince map[bool]int // keyed by "since is set"
	Merged       gh.MergedPRs
	LabelEv      *gh.LabelEvent

	Removed []string
	Checks  []gh.CheckRun
}

func (f *Fake) PullRequest(context.Context, int) (gh.PullRequest, error) { return f.PR, nil }
func (f *Fake) Files(context.Context, int) ([]gh.File, error)            { return f.FileList, nil }

func (f *Fake) AuthorCommits(_ context.Context, _, _ string, since time.Time, max int) (int, error) {
	return min(f.CommitsSince[!since.IsZero()], max), nil
}

func (f *Fake) RecentMergedPRs(context.Context, string, time.Time, int, int) (gh.MergedPRs, error) {
	return f.Merged, nil
}

func (f *Fake) LatestLabelEvent(context.Context, int, string) (gh.LabelEvent, bool, error) {
	if f.LabelEv == nil {
		return gh.LabelEvent{}, false, nil
	}
	return *f.LabelEv, true, nil
}

func (f *Fake) RemoveLabel(_ context.Context, _ int, label string) error {
	f.Removed = append(f.Removed, label)
	return nil
}

func (f *Fake) CreateCheckRun(_ context.Context, cr gh.CheckRun) error {
	f.Checks = append(f.Checks, cr)
	return nil
}
