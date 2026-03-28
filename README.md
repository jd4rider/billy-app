# Billy

[![Go](https://img.shields.io/badge/Go-1.24-00ADD8?style=flat-square&logo=go)](https://go.dev)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue?style=flat-square)](./LICENSE)

> Local AI coding assistant for the terminal.

**Billy** is a local-first coding assistant for the terminal. It uses [Ollama](https://ollama.com) by default, can also connect to OpenAI-compatible endpoints, runs fully on your machine in local mode, and stays open by default. It is built with **[Go](https://go.dev)** and the [Charm](https://charm.sh) terminal toolkit for a polished TUI workflow.

---

## Built with

| Library | Role |
|---|---|
| [Bubble Tea](https://github.com/charmbracelet/bubbletea) | TUI framework (Elm-inspired state machine) |
| [Lipgloss](https://github.com/charmbracelet/lipgloss) | Terminal styling & layout |
| [Glamour](https://github.com/charmbracelet/glamour) | Markdown + syntax highlighting in the terminal |
| [Bubbles](https://github.com/charmbracelet/bubbles) | UI components (viewport, textarea, spinner, list) |
| [Ollama](https://ollama.com) | Local LLM inference server |

---

## Features

- 💬 **Interactive TUI** — full-screen chat with scrollable history, built with [Bubble Tea](https://github.com/charmbracelet/bubbletea) and [Lipgloss](https://github.com/charmbracelet/lipgloss)
- 🤖 **Local-first AI** — Ollama by default, with optional OpenAI-compatible endpoints if you want them
- 🤖 **Agentic mode (default)** — Billy detects shell commands in responses and asks permission to run them, Copilot-style
- 🧠 **Memory system** — Billy learns about you over time; just say *"remember that..."*
- 📜 **Conversation history** — resume past sessions with an interactive `/history` picker
- 🗜 **Context compaction** — `/compact` summarizes long conversations to stay within model context
- 💾 **Session checkpoints** — `/session` saves named AI-generated checkpoints you can reload
- 🎨 **Syntax highlighting** — code blocks rendered with full markdown support
- 🔄 **Model management** — list and switch models on any backend; pull new ones directly from Ollama
- 🔓 **Open-core by default** — the full local tool ships unlocked; support Billy through setup help, sponsorship, or future convenience bundles
- 🖥 **One-shot mode** — run `billy "explain this"` directly from the terminal without launching the TUI
- ⚙️ **Simple config** — single TOML file at `~/.localai/config.toml`
- 💾 **SQLite backend** — all history, memories, and settings stored locally at `~/.localai/history.db`

---

## Requirements

- [Go 1.24+](https://go.dev/dl/) *(build from source only)*
- [Ollama](https://ollama.com) running locally on `localhost:11434` for the default local mode
- Or an OpenAI-compatible endpoint configured via `backend.type = "custom"`

---

## Installation

### macOS (Homebrew)
```bash
brew tap jd4rider/billy
brew install billy
```

### Linux / macOS (install script)
```bash
curl -fsSL https://raw.githubusercontent.com/jd4rider/billy-app/main/scripts/install.sh | bash
```
Then [install Ollama](https://ollama.com) if you haven't already.

### Windows (Scoop)
```powershell
scoop bucket add billy https://github.com/jd4rider/scoop-billy
scoop install billy
```

### Build from source
```bash
git clone https://github.com/jd4rider/billy-app.git
cd billy-app
go build -o billy ./cmd/billy
./billy
```

> Billy is still moving quickly, but the core local workflow is ready to use. [Star the repo](https://github.com/jd4rider/billy-app) to follow along.

---

## Quick Start

```bash
# Make sure Ollama is running with a model pulled
ollama pull qwen2.5-coder:14b   # Billy's default model

# Launch the TUI
billy

# First prompt ideas
billy "explain what this codebase does"

# Or use one-shot mode directly
billy read ./src
billy fix broken_code.go
cat error.log | billy "what's causing this?"

# Agentic one-shot mode
billy --agent "fix the failing tests in this repo"
billy --yolo "set up the project and keep going until it builds"
```

---

## One-shot mode

Run Billy directly from your terminal without launching the TUI:

```bash
# Ask anything
billy "what does a Dockerfile ENTRYPOINT do?"

# Analyze a file or entire directory
billy read ./src
billy read main.go

# Explain what code does
billy explain internal/tui/chat.go

# Review and suggest fixes
billy fix broken_code.go

# Run a file — Billy diagnoses errors automatically
billy run script.py

# Pipe input
cat error.log | billy "what's causing this?"
git diff | billy "summarize these changes"
```

One-shot flags:

- `--agent` enables approval-gated command execution loops in one-shot mode
- `--yolo` enables one-shot agent mode with auto-approved commands

---

## TUI Commands

Type `/` in the chat to open the **command picker** — scroll through all commands with arrow keys and press Enter to select.

### Chat & Sessions

| Command | Description |
|---|---|
| `/history` | Browse past conversations (arrow keys + Enter to resume) |
| `/resume <id>` | Jump directly to a conversation by ID |
| `/save` | Save the current conversation |
| `/clear` | Clear the current chat |
| `/compact` | Summarize & compress conversation to free up context |
| `/session` | Save a named session checkpoint (AI-generated summary) |
| `/session list` | List all saved checkpoints |
| `/session load <name>` | Restore a session from a saved checkpoint |

### AI & Models

| Command | Description |
|---|---|
| `/backend` | Show the active backend and config path |
| `/backend reload` | Reload backend settings from `~/.localai/config.toml` |
| `/model` | List models exposed by the current backend |
| `/model <name>` | Switch to a different model |
| `/pull <name>` | Download a model from the Ollama library (local backend only) |
| `/mode agent` | Enable agentic mode (default) — auto-detects and runs shell commands |
| `/mode autopilot` | Enable fully autonomous agent mode — auto-runs commands and keeps iterating |
| `/mode chat` | Disable command detection — pure conversation mode |
| `/mode teach` | Teaching mode — Socratic guidance, step-by-step |
| `/yolo` | Toggle session-wide auto-approve for all suggested commands |

### Memory

| Command | Description |
|---|---|
| `/memory` | List everything Billy remembers about you |
| `/memory forget <id>` | Remove a specific memory |
| `/memory clear` | Wipe all memories |

### Shell & Filesystem

| Command | Description |
|---|---|
| `/pwd` | Print current working directory |
| `/cd <path>` | Change directory — type `/cd ` to open live directory picker |
| `/ls [path]` | List files and directories (with counts) |
| `/git` | Show git branch, status, and recent commits |
| `/suggest <task>` | Suggest the best shell command for a natural-language task |
| `/explain <cmd>` | Explain what a shell command does, flag by flag |

> **Tip:** Type `/cd ` (with a space) and the picker pops up with all subdirectories. Use ↑↓ to navigate, `..` to go up, or keep typing to filter by name.

### Shell Execution (Agentic mode)

| Command | Description |
|---|---|
| `/run <cmd>` | Run a shell command with permission prompt |

When Billy suggests a shell command in a bash block, it will prompt:

```
┌─ Run command? ──────────────────┐
│  git commit -m "fix typo"       │
│  [Y]es  [A]lways  [N]o          │
└─────────────────────────────────┘
```

- **Y / Enter** — run once
- **A** — always run this command type for this session
- **N / S** — skip (cancels pending queue)

### Access & Support

| Command | Description |
|---|---|
| `/license` | Show Billy's current open-core access model and support links |

### General

| Command | Description |
|---|---|
| `/help` | Show all available commands |
| `/quit` | Exit Billy |
| `--version` | Print version, commit, date, and build variant |

---

### Natural language memory

You don't need a command — just tell Billy:

```
remember that I prefer TypeScript over JavaScript
save that my project uses PostgreSQL
don't forget I'm building a SaaS product
```

Billy detects the intent and stores it automatically, then injects relevant memories into future conversations.

---

## Modes

The current mode and working directory are shown in the status bar at the bottom of the TUI.

| Mode | Badge | Behaviour |
|---|---|---|
| **Agent** (default) | `[AGENT]` cyan | Detects bash blocks in responses, queues them for permission-gated execution |
| **Autopilot** | `[AUTO]` amber | Auto-runs suggested commands and feeds output back until the task settles |
| **Chat** | `[CHAT]` dim | No command detection — pure conversation |
| **Teach** | `[TEACH]` green | Socratic guidance; shows commands as "type this yourself" prompts |

---

## Sustainability

Billy currently ships fully unlocked.

You can use the full local tool for free, wire up Ollama or your own compatible backend yourself, and keep your workflow local-first.

Build software and games that are free, honest, and clean - funded by people who believe in them, not by exploiting users.

If Billy helps you, support the project through:

- setup help or guided installation
- [GitHub Sponsors](https://github.com/sponsors/jd4rider)
- [Buy Me a Coffee](https://buymeacoffee.com/jd4rider)
- future convenience bundles or supporter packs

---

## Configuration

`~/.localai/config.toml`:

```toml
[backend]
type = "ollama"
url  = "http://localhost:11434"

[ollama]
model       = "qwen2.5-coder:14b"
temperature = 0.7
```

Custom endpoint example:

```toml
[backend]
type    = "custom"
url     = "https://openrouter.ai/api/v1"
model   = "anthropic/claude-3.7-sonnet"
api_key = "sk-..."
```

| Key | Default | Description |
|---|---|---|
| `backend.type` | `ollama` | Backend (`ollama` or `custom`) |
| `backend.url` | `http://localhost:11434` | Backend base URL |
| `backend.model` | unset | Model for custom endpoints |
| `backend.api_key` | unset | API key for custom endpoints |
| `ollama.model` | `qwen2.5-coder:14b` | Default model for Ollama |
| `ollama.temperature` | `0.7` | Sampling temperature (0.0–1.0) |

Environment variable overrides:

```bash
BILLY_MODEL=llama3 billy
BILLY_BACKEND_TYPE=custom \
BILLY_BACKEND_URL=https://openrouter.ai/api/v1 \
BILLY_BACKEND_MODEL=anthropic/claude-3.7-sonnet \
BILLY_API_KEY=sk-... \
billy
```

---

## Roadmap

| Status | Feature |
|---|---|
| ✅ | Interactive TUI ([Bubble Tea](https://github.com/charmbracelet/bubbletea) + [Lipgloss](https://github.com/charmbracelet/lipgloss)) |
| ✅ | Local Ollama backend |
| ✅ | Conversation history (SQLite) |
| ✅ | Memory system (natural language detection) |
| ✅ | Interactive history picker |
| ✅ | Model list, switch & pull |
| ✅ | Agentic mode — shell command detection & permission prompts |
| ✅ | `/run` shell execution |
| ✅ | Open-core access model with optional supporter paths |
| ✅ | Binary distribution — script install, Homebrew, Scoop, .deb/.rpm/.apk |
| ✅ | [billysh.online](https://billysh.online) landing page live |
| ✅ | [Starlight docs site](https://docs.billysh.online) |
| ✅ | One-shot CLI mode (`billy "prompt"`, `billy read/explain/fix/run`) |
| ✅ | Custom / OpenAI-compatible HTTP backends |
| ✅ | Context compaction (`/compact`) with token estimate in status bar |
| ✅ | Session checkpoints (`/session`, `/session list`, `/session load`) |
| ✅ | `/pwd`, `/cd` with live directory autocomplete picker |
| ✅ | `/ls`, `/git`, `/suggest`, `/explain` shell tools |
| ✅ | Working directory shown abbreviated in status bar |
| ✅ | Teaching mode (`/mode teach`) + admin controls |
| 🔜 | Groq / Billy relay presets |
| 🔜 | Integration tests |
| 🔜 | Voice mode (Whisper + Piper TTS) |
| 🔜 | IDE plugins (VS Code, JetBrains) |
| 🔜 | Standalone chat app |
| 🔜 | iPhone companion app |

---

## Project Structure

```
billy-app/
├── cmd/
│   └── billy/          # Main entry point (TUI + one-shot dispatch)
├── internal/
│   ├── backend/        # AI backend clients (Ollama + custom OpenAI-compatible endpoints)
│   ├── config/         # TOML config + env var overrides
│   ├── launcher/       # Ollama detection and startup helpers
│   ├── license/        # Legacy activation plumbing / future supporter-key experiments
│   ├── memory/         # Memory detection & system prompt builder
│   ├── oneshot/        # Headless one-shot execution (no TUI)
│   ├── store/          # SQLite: history, memories, kv (encrypted), checkpoints
│   └── tui/            # Bubble Tea UI (chat view, history picker)
├── scripts/
│   ├── install.sh      # Main installer
│   └── fetch-ollama.sh # Helper for packaging workflows
├── .goreleaser.yml
├── go.mod
└── README.md
```

---

## Contributing

Feedback, issues, and PRs are very welcome. [Open an issue](https://github.com/jd4rider/billy-app/issues) or start a discussion.

---

## License

MIT — see [LICENSE](./LICENSE)
