# Billy.sh

[![Alpha](https://img.shields.io/badge/status-alpha-yellow?style=flat-square)](https://github.com/jd4rider/billy-app/releases)
[![Go](https://img.shields.io/badge/Go-1.24-00ADD8?style=flat-square&logo=go)](https://go.dev)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue?style=flat-square)](./LICENSE)

> Your local AI coding assistant. No subscription required.

**Billy.sh** is a Copilot CLI alternative with local [Ollama](https://ollama.com) by default and paid support for custom OpenAI-compatible endpoints. It runs entirely on your machine in local mode, works offline, and has no recurring cost for the core local experience. Built with **[Go](https://go.dev)** and the [Charm](https://charm.sh) terminal toolkit for a polished terminal experience.

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
- 🤖 **Local-first AI** — Ollama by default, with paid support for custom OpenAI-compatible endpoints
- 🤖 **Agentic mode (default)** — Billy detects shell commands in responses and asks permission to run them, Copilot-style
- 🧠 **Memory system** — Billy learns about you over time; just say *"remember that..."*
- 📜 **Conversation history** — resume past sessions with an interactive `/history` picker
- 🗜 **Context compaction** — `/compact` summarizes long conversations to stay within model context
- 💾 **Session checkpoints** — `/session` saves named AI-generated checkpoints you can reload
- 🎨 **Syntax highlighting** — code blocks rendered with full markdown support
- 🔄 **Model management** — list and switch models on any backend; pull new ones directly from Ollama
- 🔑 **License tiers** — Free / Pro / Premium / Team / Enterprise; activate with `/activate`, free a seat with `/deactivate`
- 🖥 **One-shot mode** — run `billy "explain this"` directly from the terminal without launching the TUI
- ⚙️ **Simple config** — single TOML file at `~/.localai/config.toml`
- 💾 **SQLite backend** — all history, memories, and settings stored locally at `~/.localai/history.db`

---

## Requirements

- [Go 1.24+](https://go.dev/dl/) *(build from source only)*
- [Ollama](https://ollama.com) running locally on `localhost:11434` for the default local mode
- Or a paid custom endpoint configured via `backend.type = "custom"`

---

## Installation

### macOS (Homebrew)
```bash
brew install jd4rider/billy/billy
```

### Linux / macOS (install script — slim, requires Ollama)
```bash
curl -fsSL https://raw.githubusercontent.com/jd4rider/billy-app/main/scripts/install.sh | bash
```
Then [install Ollama](https://ollama.com) if you haven't already.

### Full install (Ollama bundled — auto-starts headless)
```bash
curl -fsSL https://raw.githubusercontent.com/jd4rider/billy-app/main/scripts/install.sh | bash -s -- --full
```
The full variant (~80 MB) bundles a headless Ollama binary that Billy starts automatically — no separate install needed.

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

| Variant | Ollama | Binary size |
|---|---|---|
| Slim | Detects/prompts — you install Ollama | ~10 MB |
| Full | Bundled + auto-starts headless | ~80 MB |

> **Alpha** — stable enough to use, still moving fast. [Star the repo](https://github.com/jd4rider/billy-app) to follow along.

---

## Quick Start

```bash
# Make sure Ollama is running with a model pulled
ollama pull qwen2.5-coder:14b   # Billy's default model

# Launch the TUI
billy

# Or use one-shot mode directly
billy "explain what this codebase does"
billy read ./src
billy fix broken_code.go
cat error.log | billy "what's causing this?"
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
| `/mode chat` | Disable command detection — pure conversation mode |
| `/mode teach` | Teaching mode — Socratic guidance, step-by-step (coming soon) |

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

### License & Account

| Command | Description |
|---|---|
| `/activate` | Prompt for your Billy license key and activate this machine |
| `/deactivate` | Release this machine's seat back to your license |
| `/license` | Show current license tier and status |

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
| **Chat** | `[CHAT]` dim | No command detection — pure conversation |
| **Teach** *(coming soon)* | `[TEACH]` green | Socratic guidance; shows commands as "type this yourself" prompts |

---

## Pricing

| Tier | Price | Limits |
|---|---|---|
| **Free** | $0 | 20 messages/session, 5 history slots, local Ollama only |
| **Pro** | $19 one-time | Unlimited messages, all backends, full history, all commands |
| **Premium** | $49 one-time | Pro + future voice mode, IDE plugins, priority support |
| **Team** | ~$14/seat | Bulk seats, shared memory, admin controls |
| **Enterprise** | Custom | Unlimited seats, self-hosted, SLA — call [406-396-7246](tel:+14063967246) |

Upgrade at **[billy.sh](https://jd4rider.github.io/billy-web)**, then run `/activate` inside Billy.

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

Custom endpoint example for paid tiers:

```toml
[backend]
type    = "custom"
url     = "https://openrouter.ai/api/v1"
model   = "anthropic/claude-3.7-sonnet"
api_key = "sk-..."
```

| Key | Default | Description |
|---|---|---|
| `backend.type` | `ollama` | Backend (`ollama` or paid `custom`) |
| `backend.url` | `http://localhost:11434` | Backend base URL |
| `backend.model` | unset | Model for paid custom endpoints |
| `backend.api_key` | unset | API key for paid custom endpoints |
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
| ✅ | License system — Free / Pro / Premium / Team / Enterprise |
| ✅ | Lemon Squeezy activation with encrypted local activation storage |
| ✅ | Binary distribution — slim + fat (bundled Ollama) |
| ✅ | Homebrew tap, Scoop bucket, .deb/.rpm/.apk packages |
| ✅ | [billy.sh](https://jd4rider.github.io/billy-web) landing page live |
| ✅ | [Starlight docs site](https://jd4rider.github.io/billy-starlight) |
| ✅ | One-shot CLI mode (`billy "prompt"`, `billy read/explain/fix/run`) |
| ✅ | Paid custom / OpenAI-compatible HTTP backends |
| ✅ | Context compaction (`/compact`) with token estimate in status bar |
| ✅ | Session checkpoints (`/session`, `/session list`, `/session load`) |
| ✅ | `/pwd`, `/cd` with live directory autocomplete picker |
| ✅ | `/ls`, `/git`, `/suggest`, `/explain` shell tools |
| ✅ | Working directory shown abbreviated in status bar |
| 🔜 | Teaching mode (`/mode teach`) + admin controls |
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
│   ├── launcher/       # Ollama detection, start, embed (slim/fat)
│   ├── license/        # Lemon Squeezy activation/validation + tier constants
│   ├── memory/         # Memory detection & system prompt builder
│   ├── oneshot/        # Headless one-shot execution (no TUI)
│   ├── store/          # SQLite: history, memories, kv (encrypted), checkpoints
│   └── tui/            # Bubble Tea UI (chat view, history picker)
├── scripts/
│   ├── install.sh      # Installer (slim or --full)
│   └── fetch-ollama.sh # Download ollama binary for fat CI builds
├── .goreleaser.yml
├── go.mod
└── README.md
```

---

## Contributing

This is an alpha project — feedback, issues, and PRs are very welcome. [Open an issue](https://github.com/jd4rider/billy-app/issues) or start a discussion.

---

## License

MIT — see [LICENSE](./LICENSE)
