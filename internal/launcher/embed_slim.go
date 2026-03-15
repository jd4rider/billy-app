//go:build !fat

package launcher

// embeddedOllama is nil for the slim build.
var embeddedOllama []byte

// extractEmbedded is a no-op for slim builds.
func extractEmbedded() (string, error) { return "", nil }

// BuildVariant identifies this as the slim (no bundled Ollama) build.
const BuildVariant = "slim"
