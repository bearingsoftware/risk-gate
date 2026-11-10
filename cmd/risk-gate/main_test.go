package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const tiersDoc = `version: 1
default_tier: 1
tiers:
  2:
    paths: ["auth/**", ".github/workflows/**"]
`

func TestLintTiers(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		codeowners string
		wantCode   int
		wantOut    string
	}{
		{"all listed", "# c\nauth/** @a\n.github/workflows/** @a\n", 0, ""},
		{"extra lines fine", "* @a\nauth/** @a\n.github/workflows/** @a\ndocs/ @a\n", 0, ""},
		{"one missing", "auth/** @a\n", 1, ".github/workflows/**"},
		{"differs by slash", "/auth/ @a\n.github/workflows/** @a\n", 1, "auth/**"},
		{"empty", "", 1, "auth/**"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			tp, cp := filepath.Join(dir, "tiers.yml"), filepath.Join(dir, "CODEOWNERS")
			if err := os.WriteFile(tp, []byte(tiersDoc), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(cp, []byte(tc.codeowners), 0o644); err != nil {
				t.Fatal(err)
			}
			var out, errb bytes.Buffer
			code := execute([]string{"lint-tiers", "--tiers", tp, "--codeowners", cp}, &out, &errb, os.Getenv)
			if code != tc.wantCode {
				t.Fatalf("exit %d, want %d (%s)", code, tc.wantCode, errb.String())
			}
			if !strings.Contains(out.String(), tc.wantOut) {
				t.Fatalf("stdout = %q", out.String())
			}
		})
	}
}

func TestLintTiersErrors(t *testing.T) {
	t.Parallel()
	var out, errb bytes.Buffer
	if code := execute([]string{"lint-tiers", "--tiers", "/nonexistent", "--codeowners", "/nonexistent"}, &out, &errb, os.Getenv); code != 1 {
		t.Fatalf("missing files: exit %d", code)
	}
	if code := execute(nil, &out, &errb, os.Getenv); code != 2 {
		t.Fatalf("no args: exit %d", code)
	}
	if code := execute([]string{"nope"}, &out, &errb, os.Getenv); code != 2 {
		t.Fatalf("unknown command: exit %d", code)
	}
}

func TestScoreFlagErrors(t *testing.T) {
	t.Parallel()
	env := func(string) string { return "" }
	for name, args := range map[string][]string{
		"no repo":       {"score"},
		"bad api":       {"score", "--repo", "o/r", "--pr", "1", "--api-break", "maybe"},
		"missing token": {"score", "--repo", "o/r", "--pr", "1"},
	} {
		var out, errb bytes.Buffer
		if code := execute(args, &out, &errb, env); code != 1 {
			t.Errorf("%s: exit %d", name, code)
		}
	}
}
