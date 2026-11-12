package tiers

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	SourceCompute  = "compute"
	SourceArtifact = "artifact"

	DefaultArtifactName = "ctf-evidence"
	DefaultWaitSeconds  = 600
	MaxWaitSeconds      = 1800
)

type Evidence struct {
	Source   string            `yaml:"source"`
	Apply    bool              `yaml:"apply"`
	Compute  *EvidenceCompute  `yaml:"compute"`
	Artifact *EvidenceArtifact `yaml:"artifact"`
}

type EvidenceCompute struct {
	Package   string   `yaml:"package"`
	Specs     []string `yaml:"specs"`
	Features  []string `yaml:"features"`
	Map       string   `yaml:"map"`
	Allowlist string   `yaml:"allowlist"`
	BasePath  string   `yaml:"base_path"`
}

type EvidenceArtifact struct {
	Name        string `yaml:"name"`
	Workflow    string `yaml:"workflow"`
	WaitSeconds int    `yaml:"wait_seconds"`
}

// Every value reaches $GITHUB_OUTPUT and a shell, so each is limited to characters that need no quoting.
var (
	safePath     = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)
	safeBasePath = regexp.MustCompile(`^/?[A-Za-z0-9._/-]*$`)
	safeName     = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
	safeWorkflow = regexp.MustCompile(`^[A-Za-z0-9._-]+\.ya?ml$`)
	exactPackage = regexp.MustCompile(`^(@[a-z0-9][a-z0-9._-]*/)?[a-z0-9][a-z0-9._-]*@[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$`)
)

func (e *Evidence) validate() error {
	switch e.Source {
	case SourceCompute:
		c := e.Compute
		if c == nil || c.Package == "" || len(c.Specs) == 0 || len(c.Features) == 0 {
			return fmt.Errorf("source compute needs compute.package, compute.specs and compute.features")
		}
		if !exactPackage.MatchString(c.Package) {
			return fmt.Errorf("compute.package %q must be name@x.y.z, an exact version", c.Package)
		}
		for _, p := range append(append([]string{}, c.Specs...), c.Features...) {
			if err := relPath(p); err != nil {
				return fmt.Errorf("compute: %w", err)
			}
		}
		for _, p := range []string{c.Map, c.Allowlist} {
			if p == "" {
				continue
			}
			if err := relPath(p); err != nil {
				return fmt.Errorf("compute: %w", err)
			}
		}
		if c.BasePath != "" && (!safeBasePath.MatchString(c.BasePath) || hasDotDot(c.BasePath)) {
			return fmt.Errorf("compute.base_path %q is not a plain URL path", c.BasePath)
		}
	case SourceArtifact:
		a := e.Artifact
		if a == nil || a.Workflow == "" {
			return fmt.Errorf("source artifact needs artifact.workflow")
		}
		if !safeWorkflow.MatchString(a.Workflow) {
			return fmt.Errorf("artifact.workflow %q must be a workflow file name such as ci.yml", a.Workflow)
		}
		if a.Name != "" && !safeName.MatchString(a.Name) {
			return fmt.Errorf("artifact.name %q has characters outside A-Z a-z 0-9 . _ -", a.Name)
		}
		if a.WaitSeconds < 0 || a.WaitSeconds > MaxWaitSeconds {
			return fmt.Errorf("artifact.wait_seconds %d out of range 0..%d", a.WaitSeconds, MaxWaitSeconds)
		}
	default:
		return fmt.Errorf("unknown source %q, want compute or artifact", e.Source)
	}
	return nil
}

func relPath(p string) error {
	if strings.HasPrefix(p, "/") || hasDotDot(p) {
		return fmt.Errorf("path %q must be relative and must not contain ..", p)
	}
	if !safePath.MatchString(p) || strings.HasPrefix(p, "-") {
		return fmt.Errorf("path %q has characters outside A-Z a-z 0-9 . _ / - or starts with -", p)
	}
	return nil
}

func hasDotDot(p string) bool {
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return true
		}
	}
	return false
}

// ArtifactName returns the configured artifact name or its default.
func (a *EvidenceArtifact) ArtifactName() string {
	if a.Name == "" {
		return DefaultArtifactName
	}
	return a.Name
}

// Wait returns the configured wait in seconds or its default.
func (a *EvidenceArtifact) Wait() int {
	if a.WaitSeconds == 0 {
		return DefaultWaitSeconds
	}
	return a.WaitSeconds
}

// Plan returns the key=value lines the workflow appends to $GITHUB_OUTPUT. Validation has already
// limited every value to characters that hold no "=", newline or shell metacharacter.
func (e *Evidence) Plan() []string {
	if e == nil {
		return []string{"source=off"}
	}
	out := []string{"source=" + e.Source, fmt.Sprintf("apply=%t", e.Apply)}
	switch e.Source {
	case SourceCompute:
		c := e.Compute
		out = append(out, "package="+c.Package, "specs="+strings.Join(c.Specs, ","), "features="+strings.Join(c.Features, ","),
			"map="+c.Map, "allowlist="+c.Allowlist, "base_path="+c.BasePath)
	case SourceArtifact:
		a := e.Artifact
		out = append(out, "name="+a.ArtifactName(), "workflow="+a.Workflow, fmt.Sprintf("wait_seconds=%d", a.Wait()),
			fmt.Sprintf("wait_minutes=%d", (a.Wait()+59)/60+2))
	}
	return out
}
