package backend

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
)

// BillyError is a user-friendly error with a hint for how to fix it.
type BillyError struct {
	Message string
	Hint    string
}

func (e *BillyError) Error() string {
	if e.Hint != "" {
		return fmt.Sprintf("%s\n  💡 %s", e.Message, e.Hint)
	}
	return e.Message
}

// classifyError turns a raw backend error into a friendly BillyError.
func classifyError(err error, baseURL, model string) error {
	if err == nil {
		return nil
	}

	msg := err.Error()

	// Connection refused / Ollama not running
	var netErr *net.OpError
	if errors.As(err, &netErr) && netErr.Op == "dial" {
		return &BillyError{
			Message: fmt.Sprintf("Cannot connect to Ollama at %s", baseURL),
			Hint:    "Start Ollama with: ollama serve",
		}
	}

	// URL parse / bad config
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		if strings.Contains(msg, "connection refused") {
			return &BillyError{
				Message: fmt.Sprintf("Cannot connect to Ollama at %s", baseURL),
				Hint:    "Start Ollama with: ollama serve",
			}
		}
		if strings.Contains(msg, "no such host") {
			return &BillyError{
				Message: fmt.Sprintf("Cannot resolve host: %s", baseURL),
				Hint:    "Check BILLY_BACKEND_URL or ~/.localai/config.toml",
			}
		}
		if strings.Contains(msg, "context deadline exceeded") || strings.Contains(msg, "timeout") {
			return &BillyError{
				Message: "Request timed out waiting for Ollama",
				Hint:    "The model may be loading. Try again in a moment, or use a smaller model with /model",
			}
		}
	}

	// Context cancelled by user
	if errors.Is(err, errContextCancelled) || strings.Contains(msg, "context canceled") {
		return &BillyError{
			Message: "Request cancelled",
			Hint:    "",
		}
	}

	// Ollama-specific error strings
	if strings.Contains(msg, "model") && strings.Contains(msg, "not found") {
		return &BillyError{
			Message: fmt.Sprintf("Model '%s' is not installed", model),
			Hint:    fmt.Sprintf("Download it with: /pull %s", model),
		}
	}
	if strings.Contains(msg, "context length exceeded") || strings.Contains(msg, "context window") {
		return &BillyError{
			Message: "Conversation is too long for this model's context window",
			Hint:    "Use /clear to start a fresh conversation, or switch to a model with a larger context (/model)",
		}
	}
	if strings.Contains(msg, "out of memory") || strings.Contains(msg, "CUDA out of memory") {
		return &BillyError{
			Message: "Not enough memory to run this model",
			Hint:    "Try a smaller/quantized model: /pull mistral or /pull phi3",
		}
	}

	// Generic fallback — still wrap it for consistent display
	return &BillyError{
		Message: fmt.Sprintf("Ollama error: %s", msg),
		Hint:    "Check that Ollama is running and the model is installed (/model)",
	}
}

// errContextCancelled is a sentinel used in classifyError.
var errContextCancelled = fmt.Errorf("context canceled")
