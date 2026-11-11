// Package weights embeds the versioned scoring weights files.
package weights

import (
	"embed"
	"fmt"
)

//go:embed *.yml
var files embed.FS

// Load returns the exact bytes of weights/v<version>.yml.
func Load(version int) ([]byte, error) {
	b, err := files.ReadFile(fmt.Sprintf("v%d.yml", version))
	if err != nil {
		return nil, fmt.Errorf("unknown weights version %d: %w", version, err)
	}
	return b, nil
}
