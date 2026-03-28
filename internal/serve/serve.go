// Package serve provides the HTTP IPC server for billy-wails and other local clients.
// It listens on 127.0.0.1:7437 and exposes:
//
//	GET  /status  – tier, model, ollama reachability, version
//	POST /chat    – SSE-streamed AI response
//	GET  /history – recent conversations
//	GET  /config  – active config values
package serve

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/jonathanforrider/billy/internal/backend"
	"github.com/jonathanforrider/billy/internal/config"
	"github.com/jonathanforrider/billy/internal/license"
	"github.com/jonathanforrider/billy/internal/store"
)

const addr = "127.0.0.1:7437"

// Server is the IPC HTTP server.
type Server struct {
	cfg     *config.Config
	backend backend.Backend
	store   *store.Store
	lic     *license.License
	version string
}

// New creates a Server. store may be nil if history is unavailable.
func New(cfg *config.Config, b backend.Backend, s *store.Store, lic *license.License, version string) *Server {
	return &Server{cfg: cfg, backend: b, store: s, lic: lic, version: version}
}

// Run starts the HTTP server and blocks until ctx is cancelled.
func (srv *Server) Run(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/status", srv.handleStatus)
	mux.HandleFunc("/chat", srv.handleChat)
	mux.HandleFunc("/history", srv.handleHistory)
	mux.HandleFunc("/config", srv.handleConfig)

	hs := &http.Server{
		Addr:    addr,
		Handler: cors(mux),
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("billy serve: cannot bind %s: %w", addr, err)
	}

	fmt.Printf("billy serve: listening on http://%s\n", addr)

	errCh := make(chan error, 1)
	go func() { errCh <- hs.Serve(ln) }()

	select {
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		return hs.Shutdown(shutCtx)
	case err := <-errCh:
		return err
	}
}

// ---------------------------------------------------------------------------
// Handlers
// ---------------------------------------------------------------------------

type statusResponse struct {
	Tier    string `json:"tier"`
	Model   string `json:"model"`
	Version string `json:"version"`
	Ollama  bool   `json:"ollama"`
}

func (srv *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	tier := "open"
	if srv.lic != nil {
		tier = string(srv.lic.EffectiveTier())
	}

	ollamaOK := checkOllama(srv.cfg.Backend.URL)

	writeJSON(w, statusResponse{
		Tier:    tier,
		Model:   srv.backend.CurrentModel(),
		Version: srv.version,
		Ollama:  ollamaOK,
	})
}

type chatRequest struct {
	Messages []backend.Message `json:"messages"`
	Model    string            `json:"model"`
}

func (srv *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	if req.Model != "" {
		srv.backend.SetModel(req.Model)
	}

	// SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, canFlush := w.(http.Flusher)

	opts := backend.ChatOptions{
		Temperature: srv.cfg.Ollama.Temperature,
		NumPredict:  srv.cfg.Ollama.NumPredict,
		Stream:      true,
	}

	_, err := srv.backend.StreamChat(r.Context(), req.Messages, opts, func(token string) {
		fmt.Fprintf(w, "data: %s\n\n", token)
		if canFlush {
			flusher.Flush()
		}
	})
	if err != nil {
		fmt.Fprintf(w, "data: [ERROR] %s\n\n", err.Error())
		if canFlush {
			flusher.Flush()
		}
		return
	}
	fmt.Fprint(w, "data: [DONE]\n\n")
	if canFlush {
		flusher.Flush()
	}
}

type conversationSummary struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Model     string `json:"model"`
	UpdatedAt string `json:"updatedAt"`
}

func (srv *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if srv.store == nil {
		writeJSON(w, []conversationSummary{})
		return
	}
	convs, err := srv.store.ListConversations()
	if err != nil {
		log.Printf("serve/history: %v", err)
		writeJSON(w, []conversationSummary{})
		return
	}
	out := make([]conversationSummary, 0, len(convs))
	for _, c := range convs {
		out = append(out, conversationSummary{
			ID:        c.ID,
			Title:     c.Title,
			Model:     c.Model,
			UpdatedAt: c.UpdatedAt.Format(time.RFC3339),
		})
	}
	writeJSON(w, out)
}

type configResponse struct {
	BackendType string  `json:"backendType"`
	BackendURL  string  `json:"backendURL"`
	Model       string  `json:"model"`
	Temperature float64 `json:"temperature"`
	NumPredict  int     `json:"numPredict"`
}

func (srv *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, configResponse{
		BackendType: srv.cfg.Backend.Type,
		BackendURL:  srv.cfg.Backend.URL,
		Model:       srv.backend.CurrentModel(),
		Temperature: srv.cfg.Ollama.Temperature,
		NumPredict:  srv.cfg.Ollama.NumPredict,
	})
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// cors wraps a handler and allows requests from localhost (for billy-wails).
func cors(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "http://localhost:34115") // wails dev
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// checkOllama does a fast HEAD/GET to the Ollama base URL.
func checkOllama(ollamaURL string) bool {
	if ollamaURL == "" {
		ollamaURL = "http://localhost:11434"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ollamaURL, nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return true
}
