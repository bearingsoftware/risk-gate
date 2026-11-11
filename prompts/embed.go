// Package prompts embeds the versioned classifier prompt and its output schema.
package prompts

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
)

//go:embed classifier
var files embed.FS

// Classifier is one prompt version; the version is its directory name.
type Classifier struct {
	Version string
	System  []byte
	Schema  []byte
}

func LoadClassifier(version string) (Classifier, error) {
	sys, err := files.ReadFile("classifier/" + version + "/system.md")
	if err != nil {
		return Classifier{}, fmt.Errorf("unknown classifier prompt %q: %w", version, err)
	}
	schema, err := files.ReadFile("classifier/" + version + "/schema.json")
	if err != nil {
		return Classifier{}, fmt.Errorf("classifier prompt %q schema: %w", version, err)
	}
	return Classifier{Version: version, System: sys, Schema: schema}, nil
}

// SHA256 covers system.md followed by schema.json.
func (c Classifier) SHA256() string {
	h := sha256.New()
	h.Write(c.System)
	h.Write(c.Schema)
	return hex.EncodeToString(h.Sum(nil))
}
