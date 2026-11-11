package main

import (
	"testing"
	"time"

	"github.com/bearingsoftware/risk-gate/internal/score"
)

var created = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

func commitBy(login string, at time.Time) commitRec {
	c := commitRec{}
	c.Author = &struct {
		Login string `json:"login"`
	}{Login: login}
	c.Commit.Committer.Date = at
	return c
}

func TestAuthorHistory(t *testing.T) {
	t.Parallel()
	inWindow := func(n int) []commitRec {
		var out []commitRec
		for i := range n {
			out = append(out, commitBy("alice", created.AddDate(0, 0, -1-i)))
		}
		return out
	}
	tests := []struct {
		name       string
		commits    []commitRec
		wantTenure int
		wantEver   int
	}{
		{"four commits in window", inWindow(4), 4, 4},
		{"five commits in window", inWindow(5), 5, 5},
		{"commit after created not counted", append(inWindow(4), commitBy("alice", created.Add(time.Hour))), 4, 4},
		{"commit older than 180d counts only as ever", []commitRec{commitBy("alice", created.AddDate(0, 0, -181))}, 0, 1},
		{"other author not counted", append(inWindow(4), commitBy("bob", created.AddDate(0, 0, -2))), 4, 4},
		{"no commits", nil, 0, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tenure, ever := authorHistory(tc.commits, "alice", created)
			if tenure != tc.wantTenure || ever != tc.wantEver {
				t.Fatalf("authorHistory = (%d, %d), want (%d, %d)", tenure, ever, tc.wantTenure, tc.wantEver)
			}
		})
	}
}

func TestHotFileCount(t *testing.T) {
	t.Parallel()
	lb := score.HotLookback{Days: 30, MaxPRs: 100, TouchedByMoreThan: 3}
	others := func(n int, file string, at time.Time) []mergedPR {
		var out []mergedPR
		for i := range n {
			out = append(out, mergedPR{Number: 100 + i, MergedAt: at, Files: []string{file}})
		}
		return out
	}
	before := created.AddDate(0, 0, -2)
	tests := []struct {
		name  string
		prior []mergedPR
		self  int
		want  int
	}{
		{"three other PRs is not hot", others(3, "pkg/x.go", before), 1, 0},
		{"four other PRs is hot", others(4, "pkg/x.go", before), 1, 1},
		{"self not counted", append(others(3, "pkg/x.go", before), mergedPR{Number: 1, MergedAt: before, Files: []string{"pkg/x.go"}}), 1, 0},
		{"merged after created not counted", append(others(3, "pkg/x.go", before), mergedPR{Number: 200, MergedAt: created.Add(time.Hour), Files: []string{"pkg/x.go"}}), 1, 0},
		{"merged before 30d window not counted", append(others(3, "pkg/x.go", before), mergedPR{Number: 201, MergedAt: created.AddDate(0, 0, -31), Files: []string{"pkg/x.go"}}), 1, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := hotFileCount(tc.prior, tc.self, created, []string{"pkg/x.go"}, lb)
			if got != tc.want {
				t.Fatalf("hotFileCount = %d, want %d", got, tc.want)
			}
		})
	}
}
