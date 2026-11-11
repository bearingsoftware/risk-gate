package main

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/bearingsoftware/risk-gate/internal/score"
)

const tenureDays = 180

type mergedPR struct {
	Number   int
	MergedAt time.Time
	Files    []string
}

// authorHistory counts an author's commits before created: tenure over the live 180-day lookback,
// ever over every commit in the cache.
func authorHistory(commits []commitRec, login string, created time.Time) (tenure, ever int) {
	since := created.AddDate(0, 0, -tenureDays)
	for _, c := range commits {
		if c.Author == nil || c.Author.Login != login {
			continue
		}
		d := c.Commit.Committer.Date
		if !d.Before(created) {
			continue
		}
		ever++
		if !d.Before(since) {
			tenure++
		}
	}
	return tenure, ever
}

// hotFileCount mirrors the live rule: files of this PR that more than TouchedByMoreThan of the
// most recent MaxPRs PRs merged in the Days before created (excluding self) also touched.
func hotFileCount(prior []mergedPR, self int, created time.Time, files []string, lb score.HotLookback) int {
	since := created.AddDate(0, 0, -lb.Days)
	var recent []mergedPR
	for _, p := range prior {
		if p.Number != self && !p.MergedAt.Before(since) && p.MergedAt.Before(created) {
			recent = append(recent, p)
		}
	}
	slices.SortFunc(recent, func(a, b mergedPR) int { return b.MergedAt.Compare(a.MergedAt) })
	if len(recent) > lb.MaxPRs {
		recent = recent[:lb.MaxPRs]
	}
	touched := map[string]int{}
	for _, p := range recent {
		for _, f := range slices.Compact(slices.Sorted(slices.Values(p.Files))) {
			touched[f]++
		}
	}
	n := 0
	for _, f := range files {
		if touched[f] > lb.TouchedByMoreThan {
			n++
		}
	}
	return n
}

type variant struct {
	Name    string
	Weights *score.Weights
}

// variants returns v1 as-is plus four in-memory weight edits; no change to internal/score is needed.
func variants(base *score.Weights) ([]variant, error) {
	if len(base.Size) != 4 {
		return nil, fmt.Errorf("size bands: want 4, have %d", len(base.Size))
	}
	with := func(points ...int) []score.SizeBand {
		bands := slices.Clone(base.Size)
		for i := range bands {
			bands[i].Points = points[i]
		}
		return bands
	}
	a := *base
	a.Size = with(0, 1, 2, 2)
	b := *base
	b.Size = with(0, 1, 1, 2)
	c := *base
	c.NoTests.Points = 1
	d := *base
	d.AuthorTenure.Points = 0
	return []variant{
		{"v1", base},
		{"a_size_0_1_2_2", &a},
		{"b_size_0_1_1_2", &b},
		{"c_no_tests_1", &c},
		{"d_author_tenure_0", &d},
	}, nil
}

type signalCount struct {
	Signal string `json:"signal"`
	Count  int    `json:"count"`
}

type laneSummary struct {
	Count            int            `json:"count"`
	Tier0            int            `json:"tier0"`
	Tier1            int            `json:"tier1"`
	Tier2            int            `json:"tier2"`
	Tier2Pct         float64        `json:"tier2_pct"`
	Tier2Over70Pct   bool           `json:"tier2_over_70pct"`
	Tier2FloorDriven map[string]int `json:"tier2_floor_driven,omitempty"`
	Tier2ScoreDriven int            `json:"tier2_score_driven,omitempty"`
	ScoreHistogram   map[int]int    `json:"score_histogram,omitempty"`
	Tier2Drivers     []signalCount  `json:"tier2_drivers,omitempty"`
	TopSignals       []signalCount  `json:"top_signals,omitempty"`
}

type scoredPR struct {
	Number  int
	Lane    string
	Results map[string]score.Result
}

// summarise tallies every PR in one variant. Signal detail is only built for v1.
func summarise(prs []scoredPR, variantName string, detailed bool) map[string]*laneSummary {
	out := map[string]*laneSummary{}
	signals := map[string]map[string]int{}
	drivers := map[string]map[string]int{}
	for _, sp := range prs {
		r := sp.Results[variantName]
		s := out[r.Lane]
		if s == nil {
			s = &laneSummary{Tier2FloorDriven: map[string]int{}}
			s.ScoreHistogram = map[int]int{}
			out[r.Lane] = s
			signals[r.Lane] = map[string]int{}
			drivers[r.Lane] = map[string]int{}
		}
		s.Count++
		switch r.DetTier {
		case 0:
			s.Tier0++
		case 1:
			s.Tier1++
		case 2:
			s.Tier2++
		}
		if !detailed {
			continue
		}
		s.ScoreHistogram[r.Score]++
		seen := map[string]bool{}
		for _, f := range r.Floors {
			seen["floor:"+f.Rule] = true
		}
		for _, it := range r.ScoreItems {
			seen["score:"+it.Signal] = true
		}
		for k := range seen {
			signals[r.Lane][k]++
		}
		if r.DetTier != 2 {
			continue
		}
		first := ""
		for _, f := range r.Floors {
			if f.Tier == 2 {
				first = f.Rule
				break
			}
		}
		if first != "" {
			s.Tier2FloorDriven[first]++
		} else {
			s.Tier2ScoreDriven++
		}
		for k := range seen {
			drivers[r.Lane][k]++
		}
	}
	for lane, s := range out {
		if s.Count > 0 {
			s.Tier2Pct = math.Round(float64(s.Tier2)*1000/float64(s.Count)) / 10
		}
		s.Tier2Over70Pct = lane == "human" && s.Tier2Pct > 70
		if !detailed {
			s.Tier2FloorDriven, s.ScoreHistogram = nil, nil
			continue
		}
		s.Tier2Drivers = topSignals(drivers[lane], 10)
		s.TopSignals = topSignals(signals[lane], 10)
	}
	return out
}

func topSignals(m map[string]int, n int) []signalCount {
	var out []signalCount
	for k, v := range m {
		out = append(out, signalCount{Signal: k, Count: v})
	}
	slices.SortFunc(out, func(a, b signalCount) int {
		if a.Count != b.Count {
			return b.Count - a.Count
		}
		return strings.Compare(a.Signal, b.Signal)
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}
