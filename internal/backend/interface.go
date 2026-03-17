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

// PullProgress reports streaming progress during a model pull.
type PullProgress struct {
	Status    string
	Completed int64
	Total     int64
}

// Backend is the common interface all AI providers must implement.
type Backend interface {
	Chat(ctx context.Context, history []Message, opts ChatOptions) (string, error)
	// StreamChat sends a request with streaming enabled and calls onToken for each
	// token as it arrives. The full response is returned when complete.
	StreamChat(ctx context.Context, history []Message, opts ChatOptions, onToken func(string)) (string, error)
	ListModels(ctx context.Context) ([]Model, error)
	PullModel(ctx context.Context, name string, progress chan<- PullProgress) error
	SetModel(model string)
	CurrentModel() string
	Name() string
}
