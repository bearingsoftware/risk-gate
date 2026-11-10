// Package tiers parses .github/review-tiers.yml and matches paths against its globs.
package tiers

import (
	"path"
	"strings"
)

// Match reports whether a repo-relative path matches a gitignore-style glob.
// A pattern without a slash matches at any depth; otherwise it is anchored at
// the repo root. "**" spans directories, and a matched directory matches
// everything beneath it.
func Match(pattern, name string) bool {
	pattern = strings.TrimSuffix(pattern, "/")
	segs := strings.Split(name, "/")
	if !strings.Contains(strings.TrimPrefix(pattern, "/"), "/") && pattern != "**" {
		p := strings.TrimPrefix(pattern, "/")
		if strings.HasPrefix(pattern, "/") {
			return segMatch(p, segs[0])
		}
		for _, s := range segs {
			if segMatch(p, s) {
				return true
			}
		}
		return false
	}
	return matchSegs(strings.Split(strings.TrimPrefix(pattern, "/"), "/"), segs)
}

func matchSegs(p, s []string) bool {
	if len(p) == 0 {
		return true
	}
	if p[0] == "**" {
		if len(p) == 1 {
			return len(s) > 0
		}
		for i := 0; i <= len(s); i++ {
			if matchSegs(p[1:], s[i:]) {
				return true
			}
		}
		return false
	}
	if len(s) == 0 || !segMatch(p[0], s[0]) {
		return false
	}
	return matchSegs(p[1:], s[1:])
}

func segMatch(pattern, seg string) bool {
	ok, err := path.Match(pattern, seg)
	return err == nil && ok
}

func validGlob(pattern string) bool {
	for _, seg := range strings.Split(pattern, "/") {
		if _, err := path.Match(seg, ""); err != nil {
			return false
		}
	}
	return true
}
