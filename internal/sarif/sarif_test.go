package sarif

import "testing"

func TestHighAndNewHigh(t *testing.T) {
	t.Parallel()
	doc := `{"runs":[{"tool":{"driver":{"rules":[
	  {"id":"sev","properties":{"security-severity":"7.0"}},
	  {"id":"low","properties":{"security-severity":"6.9"}},
	  {"id":"dflt","defaultConfiguration":{"level":"error"}}]}},
	 "results":[
	  {"ruleId":"sev","level":"warning","message":{"text":"a"},"locations":[{"physicalLocation":{"artifactLocation":{"uri":"x.go"}}}]},
	  {"ruleId":"low","level":"warning","message":{"text":"a"}},
	  {"ruleId":"dflt","message":{"text":"a"}},
	  {"ruleId":"err","level":"error","message":{"text":"a"},"properties":{"security-severity":3}},
	  {"ruleId":"num","level":"note","message":{"text":"a"},"properties":{"security-severity":9.1}}]}]}`
	fs, err := Parse([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"sev": true, "low": false, "dflt": true, "err": true, "num": true}
	for _, f := range fs {
		if f.High != want[f.RuleID] {
			t.Errorf("%s high = %v", f.RuleID, f.High)
		}
	}
	if n := NewHigh(fs[:2], fs); n != 3 {
		t.Fatalf("new high = %d", n)
	}
	if n := NewHigh(fs, fs); n != 0 {
		t.Fatalf("new high vs itself = %d", n)
	}
	if _, err := Parse([]byte("{")); err == nil {
		t.Fatal("bad json accepted")
	}
}

func TestNewHighIsAMultiset(t *testing.T) {
	t.Parallel()
	f := Finding{RuleID: "r", File: "a.go", Message: "m", High: true}
	if n := NewHigh([]Finding{f}, []Finding{f, f, f}); n != 2 {
		t.Fatalf("3 in head vs 1 in base = %d new, want 2", n)
	}
	if n := NewHigh([]Finding{f, f}, []Finding{f}); n != 0 {
		t.Fatalf("fewer in head = %d new", n)
	}
}
