//go:build fat

package launcher

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
)

//go:embed ollama_bin
var embeddedOllama []byte

// BuildVariant identifies this as the full (Ollama-bundled) build.
const BuildVariant = "fat"

// extractEmbedded writes the embedded Ollama binary to ~/.billy/bin/ollama.
func extractEmbedded() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dest := filepath.Join(home, billyBinDir, ollamaBinName())
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return "", fmt.Errorf("creating ~/.billy/bin: %w", err)
	}
	if err := os.WriteFile(dest, embeddedOllama, 0755); err != nil {
		return "", fmt.Errorf("writing ollama binary: %w", err)
	}
	return dest, nil
}
