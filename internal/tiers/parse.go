package tiers

import (
	"bufio"
	"fmt"
	"io"
	"path"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Version     int                `yaml:"version"`
	Owner       string             `yaml:"owner"`
	DefaultTier int                `yaml:"default_tier"`
	Tiers       map[int]Tier       `yaml:"tiers"`
	Release     Release            `yaml:"release"`
	Attestation *AttestationConfig `yaml:"attestation"`
	Evidence    *Evidence          `yaml:"evidence"`
}

type Tier struct {
	Description   string        `yaml:"description"`
	Paths         []string      `yaml:"paths"`
	BotAuthors    []string      `yaml:"bot_authors"`
	BotConditions BotConditions `yaml:"bot_conditions"`
}

type BotConditions struct {
	UpdateType        string `yaml:"update_type"`
	RequireProvenance bool   `yaml:"require_provenance"`
}

type Release struct {
	Heads  []string `yaml:"heads"`
	Author string   `yaml:"author"`
	Paths  []string `yaml:"paths"`
}

type AttestationConfig struct {
	Label     string   `yaml:"label"`
	Permitted []string `yaml:"permitted"`
}

// Parse reads a review-tiers.yml document.
func Parse(data []byte) (*Config, error) {
	var c Config
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("parsing tiers file: %w", err)
	}
	if c.Version != 1 {
		return nil, fmt.Errorf("tiers file: unsupported version %d", c.Version)
	}
	if c.DefaultTier < 0 || c.DefaultTier > 2 {
		return nil, fmt.Errorf("tiers file: default_tier %d out of range", c.DefaultTier)
	}
	for n, t := range c.Tiers {
		if n < 0 || n > 2 {
			return nil, fmt.Errorf("tiers file: tier %d out of range", n)
		}
		for _, p := range t.Paths {
			if p == "" || strings.HasPrefix(p, "!") || !validGlob(p) {
				return nil, fmt.Errorf("tiers file: tier %d: invalid glob %q", n, p)
			}
		}
	}
	for _, h := range c.Release.Heads {
		if _, err := path.Match(h, ""); err != nil {
			return nil, fmt.Errorf("tiers file: release.heads: invalid pattern %q", h)
		}
	}
	for _, p := range c.Release.Paths {
		if p == "" || strings.HasPrefix(p, "!") || !validGlob(p) {
			return nil, fmt.Errorf("tiers file: release.paths: invalid glob %q", p)
		}
	}
	if a := c.Release.Author; a != strings.TrimSpace(a) || strings.ContainsAny(a, " \t\n") {
		return nil, fmt.Errorf("tiers file: release.author: invalid login %q", a)
	}
	if a := c.Attestation; a != nil && a.Label == "" {
		return nil, fmt.Errorf("tiers file: attestation.label is required")
	}
	if c.Evidence != nil {
		if err := c.Evidence.validate(); err != nil {
			return nil, fmt.Errorf("tiers file: evidence: %w", err)
		}
	}
	return &c, nil
}

// Tier returns the tier of one path: tier 2 wins over tier 0, anything else is default_tier.
func (c *Config) Tier(p string) int {
	if c.matchAny(2, p) {
		return 2
	}
	if c.matchAny(0, p) {
		return 0
	}
	return c.DefaultTier
}

func (c *Config) matchAny(tier int, p string) bool {
	for _, g := range c.Tiers[tier].Paths {
		if Match(g, p) {
			return true
		}
	}
	return false
}

func (c *Config) IsBotAuthor(login string) bool {
	for _, b := range c.Tiers[0].BotAuthors {
		if b == login {
			return true
		}
	}
	return false
}

func (c *Config) IsReleaseHead(ref string) bool {
	for _, h := range c.Release.Heads {
		if ok, _ := path.Match(h, ref); ok {
			return true
		}
	}
	return false
}

// ReleaseOnly reports whether a release-head PR by release.author changes only release.paths,
// the one case where the tier2_path floor is waived. Either setting empty turns it off.
func (c *Config) ReleaseOnly(headRef, author string, files []string) bool {
	r := c.Release
	if r.Author == "" || len(r.Paths) == 0 || len(files) == 0 || !strings.EqualFold(r.Author, author) || !c.IsReleaseHead(headRef) {
		return false
	}
	for _, f := range files {
		ok := false
		for _, g := range r.Paths {
			if Match(g, f) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

// MissingFromCodeowners returns the tier 2 globs that no CODEOWNERS line lists as its pattern.
func (c *Config) MissingFromCodeowners(r io.Reader) ([]string, error) {
	have := map[string]bool{}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		have[strings.Fields(line)[0]] = true
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("reading CODEOWNERS: %w", err)
	}
	var missing []string
	for _, g := range c.Tiers[2].Paths {
		if !have[g] {
			missing = append(missing, g)
		}
	}
	return missing, nil
}
