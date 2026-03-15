package backend

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// OllamaBackend talks to a local Ollama instance.
type OllamaBackend struct {
	baseURL string
	model   string
	client  *http.Client
}

// NewOllama creates a new Ollama backend.
func NewOllama(baseURL, model string) *OllamaBackend {
	return &OllamaBackend{
		baseURL: baseURL,
		model:   model,
		client:  &http.Client{Timeout: 120 * time.Second},
	}
}

// Ping checks whether Ollama is reachable. Returns a friendly error if not.
func (o *OllamaBackend) Ping(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.baseURL, nil)
	if err != nil {
		return classifyError(err, o.baseURL, o.model)
	}
	pingClient := &http.Client{Timeout: 3 * time.Second}
	resp, err := pingClient.Do(req)
	if err != nil {
		return classifyError(err, o.baseURL, o.model)
	}
	resp.Body.Close()
	return nil
}

func (o *OllamaBackend) Name() string         { return "ollama" }
func (o *OllamaBackend) CurrentModel() string { return o.model }
func (o *OllamaBackend) SetModel(model string) { o.model = model }

type ollamaMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ollamaChatRequest struct {
	Model    string          `json:"model"`
	Messages []ollamaMessage `json:"messages"`
	Stream   bool            `json:"stream"`
	Options  ollamaOptions   `json:"options,omitempty"`
}

type ollamaOptions struct {
	Temperature float64 `json:"temperature,omitempty"`
	NumPredict  int     `json:"num_predict,omitempty"`
}

type ollamaChatResponse struct {
	Message ollamaMessage `json:"message"`
	Done    bool          `json:"done"`
	Error   string        `json:"error,omitempty"`
}

// Chat sends a conversation history to Ollama and returns the assistant reply.
func (o *OllamaBackend) Chat(ctx context.Context, history []Message, opts ChatOptions) (string, error) {
	msgs := make([]ollamaMessage, len(history))
	for i, m := range history {
		msgs[i] = ollamaMessage{Role: m.Role, Content: m.Content}
	}

	reqBody := ollamaChatRequest{
		Model:    o.model,
		Messages: msgs,
		Stream:   false,
		Options: ollamaOptions{
			Temperature: opts.Temperature,
			NumPredict:  opts.NumPredict,
		},
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := o.client.Do(req)
	if err != nil {
		return "", classifyError(err, o.baseURL, o.model)
	}
	defer resp.Body.Close()

	var result ollamaChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", &BillyError{Message: "Unexpected response from Ollama", Hint: "Check that Ollama is up to date"}
	}

	if result.Error != "" {
		return "", classifyError(errors.New(result.Error), o.baseURL, o.model)
	}

	return result.Message.Content, nil
}

type ollamaModelsResponse struct {
	Models []struct {
		Name  string `json:"name"`
		Size  int64  `json:"size"`
	} `json:"models"`
}

// ListModels returns all models available in Ollama.
func (o *OllamaBackend) ListModels(ctx context.Context) ([]Model, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.baseURL+"/api/tags", nil)
	if err != nil {
		return nil, err
	}

	resp, err := o.client.Do(req)
	if err != nil {
		return nil, classifyError(err, o.baseURL, o.model)
	}
	defer resp.Body.Close()

	var result ollamaModelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, &BillyError{Message: "Unexpected response from Ollama", Hint: "Check that Ollama is up to date"}
	}

	models := make([]Model, len(result.Models))
	for i, m := range result.Models {
		models[i] = Model{
			Name: m.Name,
			Size: fmt.Sprintf("%.1f GB", float64(m.Size)/1e9),
		}
	}
	return models, nil
}

// PullModel downloads a model from Ollama, streaming progress to the channel.
func (o *OllamaBackend) PullModel(ctx context.Context, name string, progress chan<- PullProgress) error {
	body, _ := json.Marshal(map[string]string{"name": name, "stream": "true"})

	// Use a longer timeout for pulls — large models can take many minutes
	pullClient := &http.Client{}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+"/api/pull", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := pullClient.Do(req)
	if err != nil {
		return fmt.Errorf("connect to Ollama: %w", err)
	}
	defer resp.Body.Close()

	type pullLine struct {
		Status    string `json:"status"`
		Completed int64  `json:"completed"`
		Total     int64  `json:"total"`
		Error     string `json:"error"`
	}

	dec := json.NewDecoder(resp.Body)
	for {
		var line pullLine
		if err := dec.Decode(&line); err != nil {
			break
		}
		if line.Error != "" {
			return fmt.Errorf("ollama pull error: %s", line.Error)
		}
		if progress != nil {
			progress <- PullProgress{
				Status:    line.Status,
				Completed: line.Completed,
				Total:     line.Total,
			}
		}
		if line.Status == "success" {
			break
		}
	}
	return nil
}
