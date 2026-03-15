package config

import (
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

type BackendConfig struct {
	Type   string `toml:"type"`
	URL    string `toml:"url"`
	APIKey string `toml:"api_key"`
}

type OllamaConfig struct {
	Model       string  `toml:"model"`
	Temperature float64 `toml:"temperature"`
	NumPredict  int     `toml:"num_predict"`
}

type UIConfig struct {
	Theme              string `toml:"theme"`
	SyntaxHighlighting bool   `toml:"syntax_highlighting"`
}

type StorageConfig struct {
	HistoryFile string `toml:"history_file"`
}

type Config struct {
	Backend    BackendConfig `toml:"backend"`
	Ollama     OllamaConfig  `toml:"ollama"`
	UI         UIConfig      `toml:"ui"`
	Storage    StorageConfig `toml:"storage"`
	LicenseKey string        `toml:"license_key"`
}

func DefaultConfig() *Config {
	home, _ := os.UserHomeDir()
	return &Config{
		Backend: BackendConfig{
			Type: "ollama",
			URL:  "http://localhost:11434",
		},
		Ollama: OllamaConfig{
			Model:       "mistral",
			Temperature: 0.7,
			NumPredict:  2048,
		},
		UI: UIConfig{
			Theme:              "dark",
			SyntaxHighlighting: true,
		},
		Storage: StorageConfig{
			HistoryFile: filepath.Join(home, ".localai", "history.db"),
		},
	}
}

func Load() (*Config, error) {
	cfg := DefaultConfig()

	path, err := configPath()
	if err != nil {
		return cfg, nil
	}

	if _, err := os.Stat(path); os.IsNotExist(err) {
		return cfg, nil
	}

	if _, err := toml.DecodeFile(path, cfg); err != nil {
		return nil, err
	}

	// Environment variable overrides
	if v := os.Getenv("BILLY_BACKEND_URL"); v != "" {
		cfg.Backend.URL = v
	}
	if v := os.Getenv("BILLY_BACKEND_TYPE"); v != "" {
		cfg.Backend.Type = v
	}
	if v := os.Getenv("BILLY_API_KEY"); v != "" {
		cfg.Backend.APIKey = v
	}
	if v := os.Getenv("BILLY_MODEL"); v != "" {
		cfg.Ollama.Model = v
	}

	return cfg, nil
}

func Save(cfg *Config) error {
	path, err := configPath()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	return toml.NewEncoder(f).Encode(cfg)
}

func configPath() (string, error) {
	if v := os.Getenv("BILLY_CONFIG"); v != "" {
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".localai", "config.toml"), nil
}

func MustLoad() *Config {
	cfg, err := Load()
	if err != nil {
		return DefaultConfig()
	}
	return cfg
}
