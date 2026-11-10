package tiers

import (
	"strings"
	"testing"
)

const sample = `version: 1
owner: "@chattersley"
default_tier: 1
tiers:
  2:
    description: Sensitive.
    paths:
      - "backend/internal/auth/**"
      - ".github/workflows/**"
  0:
    description: Docs.
    paths:
      - "**/*.md"
      - "docs/**"
      - "docs/secret/**"
    bot_authors: ["renovate[bot]"]
    bot_conditions:
      update_type: patch
      require_provenance: false
release:
  heads: ["develop", "release-please--*"]
attestation:
  label: tier2-self-reviewed
  permitted: ["chattersley"]
`

func TestParseAndTier(t *testing.T) {
	t.Parallel()
	c, err := Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	if c.Attestation == nil || c.Attestation.Label != "tier2-self-reviewed" || c.Attestation.Permitted[0] != "chattersley" {
		t.Fatalf("attestation = %+v", c.Attestation)
	}
	if c.Tiers[0].BotConditions.UpdateType != "patch" {
		t.Fatalf("bot conditions = %+v", c.Tiers[0].BotConditions)
	}
	tests := []struct {
		path string
		want int
	}{
		{"backend/internal/auth/session.go", 2},
		{"README.md", 0},
		{"docs/a.txt", 0},
		{"src/main.go", 1},
		{".github/workflows/ci.yml", 2},
		{"backend/internal/auth/README.md", 2},
	}
	for _, tc := range tests {
		if got := c.Tier(tc.path); got != tc.want {
			t.Errorf("Tier(%q) = %d, want %d", tc.path, got, tc.want)
		}
	}
	if !c.IsBotAuthor("renovate[bot]") || c.IsBotAuthor("dependabot[bot]") {
		t.Error("IsBotAuthor wrong")
	}
	for ref, want := range map[string]bool{"develop": true, "release-please--branches--main": true, "feat/x": false, "developer": false} {
		if got := c.IsReleaseHead(ref); got != want {
			t.Errorf("IsReleaseHead(%q) = %v, want %v", ref, got, want)
		}
	}
}

func TestParseErrors(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"bad version":   "version: 2\ndefault_tier: 1\n",
		"bad default":   "version: 1\ndefault_tier: 3\n",
		"bad tier key":  "version: 1\ndefault_tier: 1\ntiers:\n  5:\n    paths: [a]\n",
		"negation":      "version: 1\ndefault_tier: 1\ntiers:\n  2:\n    paths: [\"!a\"]\n",
		"bad glob":      "version: 1\ndefault_tier: 1\ntiers:\n  2:\n    paths: [\"[a\"]\n",
		"unknown field": "version: 1\ndefault_tier: 1\nbogus: 1\n",
		"no label":      "version: 1\ndefault_tier: 1\nattestation:\n  permitted: [a]\n",
		"bad head glob": "version: 1\ndefault_tier: 1\nrelease:\n  heads: [\"[\"]\n",
	}
	for name, doc := range tests {
		if _, err := Parse([]byte(doc)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestMissingFromCodeowners(t *testing.T) {
	t.Parallel()
	c, err := Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	co := "# owners\n\nbackend/internal/auth/** @chattersley\n/other @x\n"
	got, err := c.MissingFromCodeowners(strings.NewReader(co))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != ".github/workflows/**" {
		t.Fatalf("missing = %v", got)
	}
}
