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
	env := func(string) string { return "tok" }
	for name, args := range map[string][]string{
		"no repo":         {"score"},
		"bad api":         {"score", "--repo", "o/r", "--pr", "1", "--api-break", "maybe"},
		"sarif base only": {"score", "--repo", "o/r", "--pr", "1", "--sarif-base", "b.sarif"},
		"sarif head only": {"score", "--repo", "o/r", "--pr", "1", "--sarif-head", "h.sarif"},
		"cov base only":   {"score", "--repo", "o/r", "--pr", "1", "--coverage-base", "b"},
		"cov head only":   {"score", "--repo", "o/r", "--pr", "1", "--coverage-head", "h"},
	} {
		var out, errb bytes.Buffer
		if code := execute(args, &out, &errb, env); code != 1 {
			t.Errorf("%s: exit %d", name, code)
		}
		if strings.HasPrefix(name, "sarif") && !strings.Contains(errb.String(), "together") ||
			strings.HasPrefix(name, "cov") && !strings.Contains(errb.String(), "together") {
			t.Errorf("%s: stderr = %q", name, errb.String())
		}
	}
	var out, errb bytes.Buffer
	if code := execute([]string{"score", "--repo", "o/r", "--pr", "1"}, &out, &errb, func(string) string { return "" }); code != 1 || !strings.Contains(errb.String(), "GITHUB_TOKEN") {
		t.Errorf("missing token: exit %d %q", code, errb.String())
	}
}

func TestWeightsFlagPrecedence(t *testing.T) {
	t.Parallel()
	tp := filepath.Join(t.TempDir(), "tiers.yml")
	if err := os.WriteFile(tp, []byte(tiersDoc), 0o644); err != nil {
		t.Fatal(err)
	}
	env := func(k string) string {
		if k == "GITHUB_TOKEN" {
			return "tok"
		}
		return ""
	}
	base := []string{"score", "--repo", "o/r", "--pr", "1", "--tiers", tp}
	for _, tc := range []struct {
		name    string
		extra   []string
		wantErr string
	}{
		{"path wins over version", []string{"--weights", "/nonexistent/w.yml", "--weights-version", "99"}, "reading weights"},
		{"unknown version", []string{"--weights-version", "99"}, "unknown weights version 99"},
	} {
		var out, errb bytes.Buffer
		if code := execute(append(append([]string{}, base...), tc.extra...), &out, &errb, env); code != 1 {
			t.Errorf("%s: exit %d", tc.name, code)
		}
		if !strings.Contains(errb.String(), tc.wantErr) {
			t.Errorf("%s: stderr = %q", tc.name, errb.String())
		}
	}
}

func TestClassifyArgErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args []string
		env  map[string]string
	}{
		{"no flags", []string{"classify"}, nil},
		{"no decision", []string{"classify", "--repo", "o/r", "--pr", "1"}, nil},
		{"no token", []string{"classify", "--repo", "o/r", "--pr", "1", "--decision", "d.json"}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var out, errb bytes.Buffer
			if code := execute(tc.args, &out, &errb, func(k string) string { return tc.env[k] }); code != 1 {
				t.Fatalf("exit %d (%s)", code, errb.String())
			}
		})
	}
}

func TestEvidencePlanCmd(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, tiers, want string
		code              int
	}{
		{"absent", tiersDoc, "source=off\n", 0},
		{"artifact", tiersDoc + "evidence:\n  source: artifact\n  artifact:\n    workflow: ci.yml\n",
			"source=artifact\napply=false\nname=ctf-evidence\nworkflow=ci.yml\nwait_seconds=600\n", 0},
		{"compute", tiersDoc + "evidence:\n  source: compute\n  apply: true\n  compute:\n    package: \"x@1.2.3\"\n    specs: [a.yaml, b.yaml]\n    features: [f]\n",
			"source=compute\napply=true\npackage=x@1.2.3\nspecs=a.yaml,b.yaml\nfeatures=f\nmap=\nallowlist=\nbase_path=\n", 0},
		{"invalid block is fatal", tiersDoc + "evidence:\n  source: nope\n", "", 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := filepath.Join(t.TempDir(), "tiers.yml")
			if err := os.WriteFile(p, []byte(tc.tiers), 0o644); err != nil {
				t.Fatal(err)
			}
			var out, errb bytes.Buffer
			if code := execute([]string{"evidence-plan", "--tiers", p}, &out, &errb, os.Getenv); code != tc.code {
				t.Fatalf("exit %d (%s)", code, errb.String())
			}
			if out.String() != tc.want {
				t.Fatalf("stdout = %q, want %q", out.String(), tc.want)
			}
		})
	}
	var out, errb bytes.Buffer
	if code := execute([]string{"evidence-plan", "--tiers", "/nonexistent"}, &out, &errb, os.Getenv); code != 1 {
		t.Fatalf("missing file: exit %d", code)
	}
}

func TestScoreEvidenceFlagErrors(t *testing.T) {
	t.Parallel()
	env := func(string) string { return "tok" }
	base := []string{"score", "--repo", "o/r", "--pr", "1"}
	for name, extra := range map[string][]string{
		"bad source":         {"--evidence-source", "magic"},
		"dir without source": {"--evidence-dir", "d"},
		"apply no source":    {"--evidence-apply"},
		"error no source":    {"--evidence-error", "compute_failed"},
		"unknown reason":     {"--evidence-source", "compute", "--evidence-error", "boom"},
		"not_configured set": {"--evidence-source", "compute", "--evidence-error", "not_configured"},
	} {
		var out, errb bytes.Buffer
		if code := execute(append(append([]string{}, base...), extra...), &out, &errb, env); code != 1 || !strings.Contains(errb.String(), "evidence") {
			t.Errorf("%s: exit %d stderr %q", name, code, errb.String())
		}
	}
}
