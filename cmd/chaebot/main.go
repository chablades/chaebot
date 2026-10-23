// Command chaebot runs the Chaebot chat bot.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"chaebot/internal/chat"
	"chaebot/internal/commands"
	"chaebot/internal/timers"
	"chaebot/internal/twitch"
)

func main() {
	if err := loadDotEnv(".env"); err != nil {
		log.Fatal(err)
	}
	username := requireEnv("CHAEBOT_TWITCH_USERNAME")
	channel := requireEnv("CHAEBOT_TWITCH_CHANNEL")

	dataDir, err := dataDir()
	if err != nil {
		log.Fatal(err)
	}
	token, refresh, err := twitchAuth(filepath.Join(dataDir, "twitch_token.json"))
	if err != nil {
		log.Fatal(err)
	}
	store, err := commands.Open(filepath.Join(dataDir, "commands.json"))
	if err != nil {
		log.Fatal(err)
	}
	sched, err := timers.Open(filepath.Join(dataDir, "timers.json"))
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("loaded %d custom commands and %d timers from %s", len(store.Triggers()), sched.Len(), dataDir)

	handlers := []chat.Handler{
		sched, // first, so it sees every message to measure activity
		commands.NewHandler(store, timers.Builtins...),
	}

	tw := &twitch.Client{Username: username, Token: token, Channel: channel, Refresh: refresh}
	tw.OnMessage = func(msg chat.Message) {
		chat.Dispatch(handlers, msg, tw.Say)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	go sched.Run(ctx, map[chat.Platform]func(string){chat.Twitch: tw.Say})
	if err := tw.Run(ctx); err != nil {
		log.Fatal(err)
	}
}

// dataDir is where Chaebot keeps its saved state, overridable with
// CHAEBOT_DATA_DIR.
func dataDir() (string, error) {
	if dir := os.Getenv("CHAEBOT_DATA_DIR"); dir != "" {
		return dir, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "Chaebot"), nil
}

// loadDotEnv sets KEY=VALUE pairs from path as environment variables,
// without overriding ones already set. A missing file is not an error.
func loadDotEnv(path string) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return fmt.Errorf("%s:%d: expected KEY=VALUE", path, i+1)
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if n := len(value); n >= 2 && (value[0] == '"' || value[0] == '\'') && value[n-1] == value[0] {
			value = value[1 : n-1]
		}
		if _, set := os.LookupEnv(key); !set {
			os.Setenv(key, value)
		}
	}
	return nil
}

func requireEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("%s must be set", key)
	}
	return v
}
