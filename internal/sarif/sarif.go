// Package sarif extracts the findings risk-gate compares between base and head.
package sarif

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
)

type Finding struct {
	RuleID  string
	File    string
	Message string
	High    bool
}

func (f Finding) Key() string { return f.RuleID + "\x00" + f.File + "\x00" + f.Message }

type doc struct {
	Runs []struct {
		Tool struct {
			Driver struct {
				Rules []rule `json:"rules"`
			} `json:"driver"`
		} `json:"tool"`
		Results []struct {
			RuleID  string `json:"ruleId"`
			Level   string `json:"level"`
			Message struct {
				Text string `json:"text"`
			} `json:"message"`
			Locations []struct {
				Physical struct {
					Artifact struct {
						URI string `json:"uri"`
					} `json:"artifactLocation"`
				} `json:"physicalLocation"`
			} `json:"locations"`
			Properties map[string]any `json:"properties"`
		} `json:"results"`
	} `json:"runs"`
}

type rule struct {
	ID     string `json:"id"`
	Config struct {
		Level string `json:"level"`
	} `json:"defaultConfiguration"`
	Properties map[string]any `json:"properties"`
}

// ParseFile reads a SARIF file. A finding is high when its level is error
// or its security-severity is at least 7.0.
func ParseFile(p string) ([]Finding, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("reading sarif: %w", err)
	}
	return Parse(b)
}

func Parse(b []byte) ([]Finding, error) {
	var d doc
	if err := json.Unmarshal(b, &d); err != nil {
		return nil, fmt.Errorf("parsing sarif: %w", err)
	}
	var out []Finding
	for _, run := range d.Runs {
		rules := map[string]rule{}
		for _, r := range run.Tool.Driver.Rules {
			rules[r.ID] = r
		}
		for _, res := range run.Results {
			r := rules[res.RuleID]
			level := res.Level
			if level == "" {
				level = r.Config.Level
			}
			sev := severity(res.Properties)
			if sev == 0 {
				sev = severity(r.Properties)
			}
			f := Finding{RuleID: res.RuleID, Message: res.Message.Text, High: level == "error" || sev >= 7.0}
			if len(res.Locations) > 0 {
				f.File = res.Locations[0].Physical.Artifact.URI
			}
			out = append(out, f)
		}
	}
	return out, nil
}

func severity(props map[string]any) float64 {
	switch v := props["security-severity"].(type) {
	case string:
		f, _ := strconv.ParseFloat(v, 64)
		return f
	case float64:
		return v
	}
	return 0
}

// NewHigh counts high findings in head that are absent from base, keyed on ruleId, file and message.
func NewHigh(base, head []Finding) int {
	seen := map[string]bool{}
	for _, f := range base {
		seen[f.Key()] = true
	}
	n := 0
	for _, f := range head {
		if f.High && !seen[f.Key()] {
			n++
		}
	}
	return n
}
