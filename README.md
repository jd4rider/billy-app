# Billy.sh

> Your local AI coding assistant. No subscription required.

**Billy.sh** is an open-source Copilot CLI alternative powered by local Ollama — runs on your machine, works offline, and costs nothing after the one-time purchase.

## Features (MVP)

- 💬 Interactive chat interface (terminal TUI)
- 🤖 Local AI via Ollama — fully offline, no API keys
- 📝 Persistent conversation history
- 🎨 Syntax highlighting for code blocks
- ⚙️ Simple TOML config at `~/.localai/config.toml`

## Coming Soon

- 🔌 Multiple backends: Groq, custom servers, Billy.sh cloud
- 🎙️ Voice mode (Whisper + Piper TTS)
- 📦 Bundled Ollama installer

## Installation

```bash
# macOS / Linux (coming soon)
curl -fsSL https://billy.sh/install | bash
```

## Quick Start

```bash
billy
```

## Commands

| Command | Description |
|---------|-------------|
| `/ask <query>` | Ask a question |
| `/code <query>` | Get code suggestions |
| `/explain` | Explain selected code |
| `/models` | List available models |
| `/save` | Save conversation |
| `/clear` | Clear history |
| `/help` | Show all commands |

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

## License

MIT — see [LICENSE](./LICENSE)
