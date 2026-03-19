package backend

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/jonathanforrider/billy/internal/config"
	"github.com/jonathanforrider/billy/internal/license"
)

const (
	defaultOllamaURL = "http://localhost:11434"
	customBackendTip = "Activate Billy Pro or higher, or switch back to local Ollama."
)

// NormalizeType canonicalizes a backend type string for config/env parsing.
func NormalizeType(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "", "ollama":
		return "ollama"
	case "openai", "openai-compatible", "custom":
		return "custom"
	default:
		return strings.ToLower(strings.TrimSpace(kind))
	}
}

// ResolveModel returns the active model name for the configured backend.
func ResolveModel(cfg *config.Config) string {
	if NormalizeType(cfg.Backend.Type) == "custom" {
		return strings.TrimSpace(cfg.Backend.Model)
	}
	return strings.TrimSpace(cfg.Ollama.Model)
}

// NewFromConfig builds the configured backend, enforcing the paid-license gate
// for non-local providers.
func NewFromConfig(cfg *config.Config, lic *license.License) (Backend, error) {
	kind := NormalizeType(cfg.Backend.Type)
	model := ResolveModel(cfg)

	switch kind {
	case "ollama":
		baseURL := strings.TrimSpace(cfg.Backend.URL)
		if baseURL == "" {
			baseURL = defaultOllamaURL
		}
		return NewOllama(baseURL, model), nil

	case "custom":
		if lic == nil || lic.Free() {
			return nil, &BillyError{
				Message: "Custom endpoints require a paid Billy license",
				Hint:    customBackendTip,
			}
		}
		if strings.TrimSpace(cfg.Backend.URL) == "" {
			return nil, &BillyError{
				Message: "No custom backend URL configured",
				Hint:    `Set backend.url in ~/.localai/config.toml or BILLY_BACKEND_URL`,
			}
		}
		if model == "" {
			return nil, &BillyError{
				Message: "No custom backend model configured",
				Hint:    `Set backend.model in ~/.localai/config.toml or BILLY_BACKEND_MODEL`,
			}
		}
		return NewOpenAICompatibleBackend(cfg.Backend.URL, cfg.Backend.APIKey, model), nil

	default:
		return nil, &BillyError{
			Message: fmt.Sprintf("Unsupported backend type '%s'", cfg.Backend.Type),
			Hint:    `Use backend.type = "ollama" or "custom"`,
		}
	}
}

// ShouldAutoLaunchOllama reports whether Billy should auto-start a local Ollama
// instance for the current config.
func ShouldAutoLaunchOllama(cfg *config.Config) bool {
	if NormalizeType(cfg.Backend.Type) != "ollama" {
		return false
	}

	baseURL := strings.TrimSpace(cfg.Backend.URL)
	if baseURL == "" {
		baseURL = defaultOllamaURL
	}

	u, err := url.Parse(baseURL)
	if err != nil {
		return false
	}
	switch u.Hostname() {
	case "", "localhost", "127.0.0.1", "0.0.0.0":
		return true
	default:
		return false
	}
}

// IsOllamaBackend reports whether the active backend is Ollama-backed.
func IsOllamaBackend(b Backend) bool {
	_, ok := b.(*OllamaBackend)
	return ok
}
