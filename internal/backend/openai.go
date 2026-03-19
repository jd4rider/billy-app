package backend

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// OpenAICompatibleBackend talks to any OpenAI-compatible chat completion API.
type OpenAICompatibleBackend struct {
	baseURL string
	apiKey  string
	model   string
	client  *http.Client
}

// NewOpenAICompatibleBackend creates a backend for OpenAI-compatible APIs. The
// configured URL may point at the provider root or its /v1 base.
func NewOpenAICompatibleBackend(baseURL, apiKey, model string) *OpenAICompatibleBackend {
	normalized := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if !strings.HasSuffix(normalized, "/v1") {
		normalized += "/v1"
	}

	return &OpenAICompatibleBackend{
		baseURL: normalized,
		apiKey:  strings.TrimSpace(apiKey),
		model:   model,
		client:  &http.Client{Timeout: 120 * time.Second},
	}
}

func (o *OpenAICompatibleBackend) Name() string         { return "custom" }
func (o *OpenAICompatibleBackend) CurrentModel() string { return o.model }
func (o *OpenAICompatibleBackend) SetModel(model string) {
	o.model = strings.TrimSpace(model)
}

type openAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIChatRequest struct {
	Model       string          `json:"model"`
	Messages    []openAIMessage `json:"messages"`
	Temperature float64         `json:"temperature,omitempty"`
	MaxTokens   int             `json:"max_tokens,omitempty"`
	Stream      bool            `json:"stream,omitempty"`
}

type openAIChatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

type openAIStreamChunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

type openAIModelsResponse struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

func (o *OpenAICompatibleBackend) makeRequest(ctx context.Context, path string, body any, stream bool) (*http.Response, error) {
	var reqBody io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request: %w", err)
		}
		reqBody = bytes.NewReader(payload)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+path, reqBody)
	if err != nil {
		return nil, wrapCustomError(err, o.baseURL)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if stream {
		req.Header.Set("Accept", "text/event-stream")
	}
	if o.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+o.apiKey)
	}

	client := o.client
	if stream {
		client = &http.Client{}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, wrapCustomError(err, o.baseURL)
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		return nil, o.readAPIError(resp)
	}
	return resp, nil
}

func (o *OpenAICompatibleBackend) Chat(ctx context.Context, history []Message, opts ChatOptions) (string, error) {
	msgs := make([]openAIMessage, len(history))
	for i, m := range history {
		msgs[i] = openAIMessage{Role: m.Role, Content: m.Content}
	}

	resp, err := o.makeRequest(ctx, "/chat/completions", openAIChatRequest{
		Model:       o.model,
		Messages:    msgs,
		Temperature: opts.Temperature,
		MaxTokens:   opts.NumPredict,
	}, false)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result openAIChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode custom backend response: %w", err)
	}
	if len(result.Choices) == 0 {
		return "", &BillyError{
			Message: "Custom backend returned no choices",
			Hint:    "Check backend.model and backend.url in your Billy config.",
		}
	}
	return result.Choices[0].Message.Content, nil
}

func (o *OpenAICompatibleBackend) StreamChat(ctx context.Context, history []Message, opts ChatOptions, onToken func(string)) (string, error) {
	msgs := make([]openAIMessage, len(history))
	for i, m := range history {
		msgs[i] = openAIMessage{Role: m.Role, Content: m.Content}
	}

	resp, err := o.makeRequest(ctx, "/chat/completions", openAIChatRequest{
		Model:       o.model,
		Messages:    msgs,
		Temperature: opts.Temperature,
		MaxTokens:   opts.NumPredict,
		Stream:      true,
	}, true)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if !strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		var result openAIChatResponse
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			return "", fmt.Errorf("decode custom backend response: %w", err)
		}
		if len(result.Choices) == 0 {
			return "", &BillyError{
				Message: "Custom backend returned no choices",
				Hint:    "Check backend.model and backend.url in your Billy config.",
			}
		}
		text := result.Choices[0].Message.Content
		if onToken != nil && text != "" {
			onToken(text)
		}
		return text, nil
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)

	var full strings.Builder
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") || !strings.HasPrefix(line, "data:") {
			continue
		}

		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			break
		}

		var chunk openAIStreamChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			return full.String(), fmt.Errorf("decode custom backend stream: %w", err)
		}

		for _, choice := range chunk.Choices {
			text := choice.Delta.Content
			if text == "" {
				text = choice.Message.Content
			}
			if text == "" {
				continue
			}
			full.WriteString(text)
			if onToken != nil {
				onToken(text)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return full.String(), wrapCustomError(err, o.baseURL)
	}
	return full.String(), nil
}

func (o *OpenAICompatibleBackend) ListModels(ctx context.Context) ([]Model, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.baseURL+"/models", nil)
	if err != nil {
		return nil, wrapCustomError(err, o.baseURL)
	}
	req.Header.Set("Accept", "application/json")
	if o.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+o.apiKey)
	}

	resp, err := o.client.Do(req)
	if err != nil {
		return nil, wrapCustomError(err, o.baseURL)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, o.readAPIError(resp)
	}

	var result openAIModelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode custom backend models: %w", err)
	}

	models := make([]Model, len(result.Data))
	for i, model := range result.Data {
		models[i] = Model{Name: model.ID}
	}
	return models, nil
}

func (o *OpenAICompatibleBackend) PullModel(_ context.Context, _ string, _ chan<- PullProgress) error {
	return &BillyError{
		Message: "Model downloads are only supported with the local Ollama backend",
		Hint:    "Switch backend.type back to ollama or change backend.model manually.",
	}
}

func (o *OpenAICompatibleBackend) readAPIError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	msg := strings.TrimSpace(string(body))

	var payload struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &payload); err == nil && payload.Error.Message != "" {
		msg = payload.Error.Message
	}
	if msg == "" {
		msg = resp.Status
	}

	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return &BillyError{
			Message: "Authentication failed for the custom backend",
			Hint:    "Check backend.api_key or BILLY_API_KEY.",
		}
	case http.StatusNotFound:
		return &BillyError{
			Message: fmt.Sprintf("Custom backend endpoint not found at %s", resp.Request.URL.String()),
			Hint:    "Use an OpenAI-compatible base URL. Billy automatically appends /v1 if needed.",
		}
	default:
		return &BillyError{
			Message: fmt.Sprintf("Custom backend error (%d): %s", resp.StatusCode, msg),
			Hint:    "Check backend.url, backend.model, and backend.api_key.",
		}
	}
}

func wrapCustomError(err error, baseURL string) error {
	if err == nil {
		return nil
	}

	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		msg := strings.ToLower(urlErr.Error())
		switch {
		case strings.Contains(msg, "no such host"):
			return &BillyError{
				Message: fmt.Sprintf("Cannot resolve custom backend host: %s", baseURL),
				Hint:    "Check backend.url in ~/.localai/config.toml.",
			}
		case strings.Contains(msg, "connection refused"):
			return &BillyError{
				Message: fmt.Sprintf("Cannot connect to custom backend at %s", baseURL),
				Hint:    "Check that the server is reachable and the URL is correct.",
			}
		case strings.Contains(msg, "timeout"), strings.Contains(msg, "deadline exceeded"):
			return &BillyError{
				Message: "Request timed out waiting for the custom backend",
				Hint:    "Try again, or use a faster/smaller remote model.",
			}
		}
	}

	return &BillyError{
		Message: fmt.Sprintf("Custom backend error: %s", err.Error()),
		Hint:    "Check backend.url and network connectivity.",
	}
}
