// Command chaebot runs the Chaebot chat bot without the desktop UI.
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"path/filepath"

	"chaebot/internal/chat"
	"chaebot/internal/commands"
	"chaebot/internal/config"
	"chaebot/internal/twitch"
)

func main() {
	if err := config.LoadDotEnv(".env"); err != nil {
		log.Fatal(err)
	}
	username := requireEnv("CHAEBOT_TWITCH_USERNAME")
	token := requireEnv("CHAEBOT_TWITCH_TOKEN")
	channel := requireEnv("CHAEBOT_TWITCH_CHANNEL")

	dataDir, err := config.DataDir()
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
	say := func(text string) {
		if err := tw.Say(text); err != nil {
			log.Printf("twitch: %v", err)
		}
	}
	tw.OnMessage = func(msg chat.Message) {
		chat.Dispatch(handlers, msg, say)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := tw.Run(ctx); err != nil {
		log.Fatal(err)
	}
}

func requireEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("%s must be set", key)
	}
	return v
}
