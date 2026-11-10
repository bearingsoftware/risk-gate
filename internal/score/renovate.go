package score

import (
	"regexp"
	"strconv"
	"strings"
)

var changePair = regexp.MustCompile("`([^`]+)`\\s*(?:→|->)\\s*`([^`]+)`")

var digestLike = regexp.MustCompile(`^(sha256:)?[0-9a-f]{7,}$`)

var typeRank = map[string]int{"digest": 0, "patch": 1, "minor": 2, "major": 3}

// ParseRenovateUpdateType reads the Change column of a Renovate PR body and
// returns the widest update in it: major, minor, patch or digest; anything
// it cannot read is "unknown".
func ParseRenovateUpdateType(body string) string {
	col := -1
	best := ""
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "|") {
			continue
		}
		cells := strings.Split(strings.Trim(line, "|"), "|")
		if col < 0 {
			for i, c := range cells {
				if strings.EqualFold(strings.TrimSpace(c), "Change") {
					col = i
				}
			}
			continue
		}
		if col >= len(cells) || strings.Trim(cells[col], " -:") == "" {
			continue
		}
		m := changePair.FindStringSubmatch(cells[col])
		if m == nil {
			return "unknown"
		}
		t := classify(m[1], m[2])
		if t == "unknown" {
			return "unknown"
		}
		if best == "" || typeRank[t] > typeRank[best] {
			best = t
		}
	}
	if best == "" {
		return "unknown"
	}
	return best
}

func classify(from, to string) string {
	if digestLike.MatchString(from) && digestLike.MatchString(to) && !strings.Contains(from, ".") {
		return "digest"
	}
	a, b := versionParts(from), versionParts(to)
	if a == nil || b == nil {
		return "unknown"
	}
	for i := 0; i < 3; i++ {
		if a[i] == b[i] {
			continue
		}
		return [3]string{"major", "minor", "patch"}[i]
	}
	return "unknown"
}

func versionParts(v string) []int {
	v = strings.TrimLeft(v, "^~v=<> ")
	v, _, _ = strings.Cut(v, "-")
	v, _, _ = strings.Cut(v, "+")
	parts := strings.Split(v, ".")
	if len(parts) > 3 {
		parts = parts[:3]
	}
	out := make([]int, 3)
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil
		}
		out[i] = n
	}
	return out
}
