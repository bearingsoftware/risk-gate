package weights_test

import (
	"bytes"
	"crypto/sha256"
	"os"
	"testing"

	"github.com/bearingsoftware/risk-gate/internal/score"
	"github.com/bearingsoftware/risk-gate/weights"
)

func TestLoadV1MatchesFile(t *testing.T) {
	t.Parallel()
	disk, err := os.ReadFile("v1.yml")
	if err != nil {
		t.Fatal(err)
	}
	emb, err := weights.Load(1)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(disk, emb) {
		t.Fatal("embedded bytes differ from v1.yml")
	}
	w, err := score.ParseWeights(emb)
	if err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256(disk)
	if got := w.SHA256; got != hexOf(want[:]) {
		t.Fatalf("sha256 = %s", got)
	}
}

func hexOf(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 0, len(b)*2)
	for _, c := range b {
		out = append(out, digits[c>>4], digits[c&0xf])
	}
	return string(out)
}

func TestLoadUnknownVersion(t *testing.T) {
	t.Parallel()
	for _, v := range []int{0, -1, 999} {
		if _, err := weights.Load(v); err == nil {
			t.Errorf("version %d: want error", v)
		}
	}
}
