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
	CommitDate   time.Time
	ForcePush    *time.Time
	CommitsErr   error
	RemoveErr    error
	CheckErr     error
	CheckRunsErr error
	PRErr        error
	FilesErr     error
	DiffText     string
	DiffErr      error
	Existing     []gh.CheckRunInfo

	Removed []string
	Added   []string
	Checks  []gh.CheckRun
}

func (f *Fake) PullRequest(context.Context, int) (gh.PullRequest, error) { return f.PR, f.PRErr }
func (f *Fake) Files(context.Context, int) ([]gh.File, error)            { return f.FileList, f.FilesErr }

func (f *Fake) AuthorCommits(_ context.Context, _, _ string, since time.Time, max int) (int, error) {
	if f.CommitsErr != nil {
		return 0, f.CommitsErr
	}
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
	if f.RemoveErr != nil {
		return f.RemoveErr
	}
	f.Removed = append(f.Removed, label)
	return nil
}

func (f *Fake) AddLabel(_ context.Context, _ int, label string) error {
	f.Added = append(f.Added, label)
	return nil
}

func (f *Fake) CreateCheckRun(_ context.Context, cr gh.CheckRun) error {
	if f.CheckErr != nil {
		return f.CheckErr
	}
	f.Checks = append(f.Checks, cr)
	return nil
}

func (f *Fake) HeadCommitDate(context.Context, string) (time.Time, error) { return f.CommitDate, nil }

func (f *Fake) LatestForcePush(context.Context, int) (time.Time, bool, error) {
	if f.ForcePush == nil {
		return time.Time{}, false, nil
	}
	return *f.ForcePush, true, nil
}

func (f *Fake) Diff(context.Context, int) (string, error) { return f.DiffText, f.DiffErr }

func (f *Fake) CheckRuns(_ context.Context, _, name string) ([]gh.CheckRunInfo, error) {
	if f.CheckRunsErr != nil {
		return nil, f.CheckRunsErr
	}
	var out []gh.CheckRunInfo
	for _, c := range f.Existing {
		if c.Name == name {
			out = append(out, c)
		}
	}
	return out, nil
}
