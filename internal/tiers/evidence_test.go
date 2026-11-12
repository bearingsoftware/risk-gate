package tiers

import (
	"reflect"
	"strings"
	"testing"
)

const evBase = "version: 1\ndefault_tier: 1\n"

func TestParseEvidenceErrors(t *testing.T) {
	t.Parallel()
	const okCompute = "    package: \"@bearingsoftware/component-test-framework@0.3.16\"\n    specs: [\"api/openapi.yaml\"]\n    features: [\"api/features\"]\n"
	tests := []struct{ name, block, want string }{
		{"unknown source", "evidence:\n  source: magic\n", "unknown source"},
		{"empty source", "evidence:\n  apply: true\n", "unknown source"},
		{"compute without package", "evidence:\n  source: compute\n  compute:\n    specs: [a.yaml]\n    features: [f]\n", "compute.package"},
		{"compute without specs", "evidence:\n  source: compute\n  compute:\n    package: \"x@1.2.3\"\n    features: [f]\n", "compute.specs"},
		{"compute without features", "evidence:\n  source: compute\n  compute:\n    package: \"x@1.2.3\"\n    specs: [a.yaml]\n", "compute.features"},
		{"compute block missing", "evidence:\n  source: compute\n", "compute.package"},
		{"range package", "evidence:\n  source: compute\n  compute:\n    package: \"x@^1.2.3\"\n    specs: [a.yaml]\n    features: [f]\n", "exact version"},
		{"tag package", "evidence:\n  source: compute\n  compute:\n    package: \"x@latest\"\n    specs: [a.yaml]\n    features: [f]\n", "exact version"},
		{"unversioned package", "evidence:\n  source: compute\n  compute:\n    package: \"x\"\n    specs: [a.yaml]\n    features: [f]\n", "exact version"},
		{"absolute spec", "evidence:\n  source: compute\n  compute:\n" + strings.Replace(okCompute, "api/openapi.yaml", "/etc/openapi.yaml", 1), "relative"},
		{"dotdot feature", "evidence:\n  source: compute\n  compute:\n" + strings.Replace(okCompute, "api/features", "../features", 1), "relative"},
		{"dotdot inside", "evidence:\n  source: compute\n  compute:\n" + strings.Replace(okCompute, "api/openapi.yaml", "a/../../b.yaml", 1), "relative"},
		{"absolute map", "evidence:\n  source: compute\n  compute:\n" + okCompute + "    map: /m.json\n", "relative"},
		{"dotdot allowlist", "evidence:\n  source: compute\n  compute:\n" + okCompute + "    allowlist: ../a.json\n", "relative"},
		{"shell in path", "evidence:\n  source: compute\n  compute:\n" + strings.Replace(okCompute, "api/openapi.yaml", "a;rm.yaml", 1), "characters"},
		{"leading dash path", "evidence:\n  source: compute\n  compute:\n" + strings.Replace(okCompute, "api/openapi.yaml", "-x.yaml", 1), "starts with -"},
		{"bad base_path", "evidence:\n  source: compute\n  compute:\n" + okCompute + "    base_path: \"/a/../b\"\n", "base_path"},
		{"artifact without workflow", "evidence:\n  source: artifact\n  artifact:\n    name: ctf-evidence\n", "artifact.workflow"},
		{"artifact block missing", "evidence:\n  source: artifact\n", "artifact.workflow"},
		{"workflow with path", "evidence:\n  source: artifact\n  artifact:\n    workflow: .github/workflows/ci.yml\n", "workflow file name"},
		{"workflow not yaml", "evidence:\n  source: artifact\n  artifact:\n    workflow: ci\n", "workflow file name"},
		{"bad artifact name", "evidence:\n  source: artifact\n  artifact:\n    workflow: ci.yml\n    name: \"a b\"\n", "artifact.name"},
		{"wait too long", "evidence:\n  source: artifact\n  artifact:\n    workflow: ci.yml\n    wait_seconds: 1801\n", "wait_seconds"},
		{"wait negative", "evidence:\n  source: artifact\n  artifact:\n    workflow: ci.yml\n    wait_seconds: -1\n", "wait_seconds"},
		{"unknown key", "evidence:\n  source: artifact\n  artifact:\n    workflow: ci.yml\n    wait: 5\n", "wait"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := Parse([]byte(evBase + tt.block))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}

func TestEvidencePlan(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, block string
		want        []string
	}{
		{"absent", "", []string{"source=off"}},
		{"compute", `evidence:
  source: compute
  compute:
    package: "@bearingsoftware/component-test-framework@0.3.16"
    specs: ["a/openapi.yaml", "b/openapi.yaml"]
    features: ["a/features"]
    base_path: /v1
`, []string{"source=compute", "apply=false", "package=@bearingsoftware/component-test-framework@0.3.16",
			"specs=a/openapi.yaml,b/openapi.yaml", "features=a/features", "map=", "allowlist=", "base_path=/v1"}},
		{"artifact defaults", "evidence:\n  source: artifact\n  apply: true\n  artifact:\n    workflow: ci.yml\n",
			[]string{"source=artifact", "apply=true", "name=ctf-evidence", "workflow=ci.yml", "wait_seconds=600", "wait_minutes=12"}},
		{"artifact set", "evidence:\n  source: artifact\n  artifact:\n    workflow: ci.yml\n    name: ev\n    wait_seconds: 1800\n",
			[]string{"source=artifact", "apply=false", "name=ev", "workflow=ci.yml", "wait_seconds=1800", "wait_minutes=32"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c, err := Parse([]byte(evBase + tt.block))
			if err != nil {
				t.Fatal(err)
			}
			got := c.Evidence.Plan()
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("plan = %q, want %q", got, tt.want)
			}
			for _, l := range got {
				if strings.ContainsAny(l, "\n\r") || strings.Count(l, "=") != 1 {
					t.Fatalf("unsafe line %q", l)
				}
			}
		})
	}
}
