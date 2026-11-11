package score

import (
	"os"
	"testing"
)

func loadWeights(t testing.TB, path string) *Weights {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	w, err := ParseWeights(b)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return w
}

func TestWeightsFiles(t *testing.T) {
	t.Parallel()
	v1, v2 := loadWeights(t, "../../weights/v1.yml"), loadWeights(t, "../../weights/v2.yml")
	tests := []struct {
		name       string
		w          *Weights
		version    string
		lockfiles  bool
		tier1Max   int
		touchedMax int
	}{
		{"v1", v1, "1", true, 3, 3},
		{"v2", v2, "2", false, 5, 8},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if tc.w.Version != tc.version {
				t.Errorf("version %q, want %q", tc.w.Version, tc.version)
			}
			if got := tc.w.lockfilesFloor(); got != tc.lockfiles {
				t.Errorf("lockfilesFloor = %v, want %v", got, tc.lockfiles)
			}
			if tc.w.Mapping.Tier1MaxScore != tc.tier1Max {
				t.Errorf("tier1_max_score %d, want %d", tc.w.Mapping.Tier1MaxScore, tc.tier1Max)
			}
			if tc.w.HotLookback.TouchedByMoreThan != tc.touchedMax {
				t.Errorf("touched_by_more_than %d, want %d", tc.w.HotLookback.TouchedByMoreThan, tc.touchedMax)
			}
		})
	}
	if v1.SHA256 == v2.SHA256 {
		t.Error("v1 and v2 share a sha256")
	}
}
