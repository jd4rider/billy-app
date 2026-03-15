package backend

import "context"

// Message represents a single chat message.
type Message struct {
	Role    string // "user" or "assistant"
	Content string
}

// ChatOptions controls generation behaviour.
type ChatOptions struct {
	Temperature float64
	NumPredict  int
	Stream      bool
}

// Model is a model available on the backend.
type Model struct {
	Name string
	Size string
}

// Backend is the common interface all AI providers must implement.
type Backend interface {
	Chat(ctx context.Context, history []Message, opts ChatOptions) (string, error)
	ListModels(ctx context.Context) ([]Model, error)
	SetModel(model string)
	CurrentModel() string
	Name() string
}
