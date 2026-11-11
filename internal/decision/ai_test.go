package decision_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/bearingsoftware/risk-gate/internal/decision"
)

func compileAI(t *testing.T) *jsonschema.Schema {
	t.Helper()
	c := jsonschema.NewCompiler()
	c.AssertFormat()
	f, err := os.Open("../../schema/ai.v1.json")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	doc, err := jsonschema.UnmarshalJSON(f)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.AddResource("ai.v1.json", doc); err != nil {
		t.Fatal(err)
	}
	s, err := c.Compile("ai.v1.json")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func validateAI(s *jsonschema.Schema, b []byte) error {
	v, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	if err != nil {
		return err
	}
	return s.Validate(v)
}

func TestAIFixturesValidate(t *testing.T) {
	s := compileAI(t)
	paths, err := filepath.Glob("../../testdata/ai/*.json")
	if err != nil || len(paths) != 2 {
		t.Fatalf("fixtures: %v %d", err, len(paths))
	}
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if err := validateAI(s, b); err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		if _, ok := decision.ParseAICheckText(decision.AICheckText(bytes.TrimSpace(b))); !ok {
			t.Fatalf("%s does not round trip through the check text", p)
		}
	}
}

func TestAISchemaRejects(t *testing.T) {
	s := compileAI(t)
	good, err := os.ReadFile("../../testdata/ai/classified.json")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct{ name, from, to string }{
		{"classified with a skip reason", `"skip_reason":null`, `"skip_reason":"fork"`},
		{"classified without ai", `"ai":{"tier":2,`, `"ai":null,"x":{"tier":2,`},
		{"tier 3", `"tier":2,"confidence"`, `"tier":3,"confidence"`},
		{"unknown skip reason", `"status":"classified","skip_reason":null`, `"status":"skipped","skip_reason":"why"`},
		{"bad schema value", `risk-gate.ai/v1`, `risk-gate.ai/v2`},
		{"extra field in ai", `"security_concerns":[`, `"extra":1,"security_concerns":[`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			bad := strings.Replace(string(good), tc.from, tc.to, 1)
			if bad == string(good) {
				t.Fatal("replacement did not apply")
			}
			if validateAI(s, []byte(bad)) == nil {
				t.Fatalf("accepted: %s", bad)
			}
		})
	}
}

func TestParseAICheckText(t *testing.T) {
	rec := decision.AIRecord{Schema: decision.AISchema, Status: decision.AISkipped, Run: decision.Run{At: time.Unix(0, 0).UTC()}}
	b, err := rec.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := decision.ParseAICheckText(decision.AICheckText(b)); !ok {
		t.Fatal("round trip failed")
	}
	for _, bad := range []string{"", decision.AIMarker, decision.Marker + "\n```json\n{}\n```\n", decision.AIMarker + "\n```json\n{\"schema\":\"x\"}\n```\n", "x" + decision.AICheckText(b)} {
		if _, ok := decision.ParseAICheckText(bad); ok {
			t.Errorf("accepted %q", bad)
		}
	}
}
