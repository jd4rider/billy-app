# Billy.sh

> Your local AI coding assistant. No subscription required.

**Billy.sh** is a Copilot CLI alternative powered by local [Ollama](https://ollama.com) — runs entirely on your machine, works offline, and has no recurring cost. Built with Go + [Bubble Tea](https://github.com/charmbracelet/bubbletea) for a great terminal experience.

---

## Features

- 💬 **Interactive TUI** — full-screen chat interface with scrollable history
- 🤖 **Local AI via Ollama** — fully offline, no API keys required
- 🧠 **Memory system** — Billy learns about you over time; just say *"remember that..."*
- 📜 **Conversation history** — resume past sessions with an interactive `/history` picker
- 🎨 **Syntax highlighting** — code blocks rendered with full markdown support
- 🔄 **Model management** — list, switch, and pull new models right from the chat
- ⚙️ **Simple config** — single TOML file at `~/.localai/config.toml`
- 💾 **SQLite backend** — all history and memories stored locally at `~/.localai/history.db`

---

## Requirements

- [Go 1.24+](https://go.dev/dl/) (to build from source)
- [Ollama](https://ollama.com) running locally on `localhost:11434`

---

## Installation

### Build from source

```bash
git clone https://github.com/jd4rider/billy-app.git
cd billy-app
go build -o billy ./cmd/billy
./billy
```

### Install script *(coming soon)*

```bash
curl -fsSL https://billy.sh/install | bash
```

---

## Quick Start

```bash
# Make sure Ollama is running and you have at least one model pulled
ollama pull mistral

# Start Billy
billy
```

---

## Commands

| Command | Description |
|---|---|
| `/model` | List installed Ollama models |
| `/model <name>` | Switch to a different model |
| `/pull <name>` | Download a new model from the Ollama library |
| `/memory` | List everything Billy remembers about you |
| `/memory forget <id>` | Remove a specific memory |
| `/memory clear` | Wipe all memories |
| `/history` | Browse past conversations (arrow keys + Enter to load) |
| `/resume <id>` | Jump directly to a conversation by ID |
| `/save` | Save the current conversation |
| `/clear` | Clear the current chat |
| `/help` | Show all available commands |
| `/quit` | Exit Billy |

### Natural language memory

You don't need a command — just tell Billy:

```
remember that I prefer TypeScript over JavaScript
save that my project uses PostgreSQL
don't forget I'm building a SaaS product
```

Billy will detect the intent and store it automatically.

---

## Configuration

`~/.localai/config.toml`:

```toml
[backend]
type = "ollama"
url  = "http://localhost:11434"

[ollama]
model       = "mistral"
temperature = 0.7
```

| Key | Default | Description |
|---|---|---|
| `backend.type` | `ollama` | Backend to use (`ollama` only for now) |
| `backend.url` | `http://localhost:11434` | Ollama server URL |
| `ollama.model` | `mistral` | Default model to use |
| `ollama.temperature` | `0.7` | Sampling temperature (0.0–1.0) |

Environment variable overrides:

```bash
BILLY_MODEL=llama3 BILLY_BACKEND_URL=http://192.168.1.10:11434 billy
```

---

## Roadmap

| Status | Feature |
|---|---|
| ✅ | Interactive TUI (Bubble Tea) |
| ✅ | Local Ollama backend |
| ✅ | Conversation history (SQLite) |
| ✅ | Memory system |
| ✅ | History session picker |
| ✅ | Model list, switch & pull |
| ✅ | Friendly error handling |
| 🔜 | Integration tests |
| 🔜 | Context compaction / summarization |
| 🔜 | Groq & custom server backends |
| 🔜 | Billy.sh cloud SaaS backend |
| 🔜 | Voice mode (Whisper + Piper TTS) |
| 🔜 | Bundled Ollama installer |
| 🔜 | IDE plugins (VS Code, JetBrains) |
| 🔜 | Standalone chat app |
| 🔜 | iPhone companion app |

---

## Project Structure

```
billy-app/
├── cmd/billy/          # Entry point
├── internal/
│   ├── backend/        # AI backend clients (Ollama, …)
│   ├── config/         # TOML config + env var overrides
│   ├── memory/         # Memory detection & system prompt builder
│   ├── store/          # SQLite history + memory persistence
│   └── tui/            # Bubble Tea UI (chat view, history picker)
├── go.mod
└── README.md
```

---

## License

MIT — see [LICENSE](./LICENSE)
