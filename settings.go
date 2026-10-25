package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"chaebot/internal/config"
)

const settingsFile = "settings.json"

// Settings are the bot's Twitch login details, saved in the data folder.
// The token is stored in plain text, like .env.
type Settings struct {
	Username  string `json:"username"`
	Channel   string `json:"channel"`
	Token     string `json:"token"`
	AutoStart bool   `json:"autoStart"`
}

func (s Settings) complete() bool {
	return s.Username != "" && s.Channel != "" && s.Token != ""
}

func (s Settings) normalized() Settings {
	s.Username = strings.ToLower(strings.TrimSpace(s.Username))
	s.Channel = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(s.Channel), "#"))
	s.Token = strings.TrimSpace(s.Token)
	return s
}

// loadSettings reads the settings file from dir. On first run it starts
// from the CHAEBOT_TWITCH_* variables (and .env) the command-line bot uses.
func loadSettings(dir string) (Settings, error) {
	data, err := os.ReadFile(filepath.Join(dir, settingsFile))
	if errors.Is(err, os.ErrNotExist) {
		if err := config.LoadDotEnv(".env"); err != nil {
			return Settings{}, err
		}
		return Settings{
			Username: os.Getenv("CHAEBOT_TWITCH_USERNAME"),
			Channel:  os.Getenv("CHAEBOT_TWITCH_CHANNEL"),
			Token:    os.Getenv("CHAEBOT_TWITCH_TOKEN"),
		}.normalized(), nil
	}
	if err != nil {
		return Settings{}, err
	}
	var s Settings
	if err := json.Unmarshal(data, &s); err != nil {
		return Settings{}, err
	}
	return s, nil
}

// saveSettings writes atomically via a temp file, like the command store.
func saveSettings(dir string, s Settings) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, settingsFile)
	if err := os.WriteFile(path+".tmp", data, 0o600); err != nil {
		return err
	}
	return os.Rename(path+".tmp", path)
}
