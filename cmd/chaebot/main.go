// Command chaebot runs the Chaebot chat bot.
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"path/filepath"

	"chaebot/internal/chat"
	"chaebot/internal/commands"
	"chaebot/internal/twitch"
)

func main() {
	username := requireEnv("CHAEBOT_TWITCH_USERNAME")
	token := requireEnv("CHAEBOT_TWITCH_TOKEN")
	channel := requireEnv("CHAEBOT_TWITCH_CHANNEL")

	dataDir, err := dataDir()
	if err != nil {
		log.Fatal(err)
	}
	store, err := commands.Open(filepath.Join(dataDir, "commands.json"))
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("loaded %d custom commands from %s", len(store.Triggers()), dataDir)

	handlers := []chat.Handler{
		commands.NewHandler(store),
	}

	tw := &twitch.Client{Username: username, Token: token, Channel: channel}
	tw.OnMessage = func(msg chat.Message) {
		chat.Dispatch(handlers, msg, tw.Say)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
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

func requireEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("%s must be set", key)
	}
	return v
}
