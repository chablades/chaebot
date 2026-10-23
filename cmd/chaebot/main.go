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
	"strconv"
	"strings"
	"time"

	"chaebot/internal/chat"
	"chaebot/internal/commands"
	"chaebot/internal/timers"
	"chaebot/internal/twitch"
	"chaebot/internal/youtube"
)

func main() {
	if err := loadDotEnv(".env"); err != nil {
		log.Fatal(err)
	}
	dataDir, err := dataDir()
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

	var platforms []platform
	if os.Getenv("CHAEBOT_TWITCH_USERNAME") != "" {
		tw, err := newTwitch(filepath.Join(dataDir, "twitch_token.json"))
		if err != nil {
			log.Fatal(err)
		}
		tw.OnMessage = func(msg chat.Message) { chat.Dispatch(handlers, msg, tw.Say) }
		platforms = append(platforms, platform{chat.Twitch, tw.Run, tw.Say})
	}
	if yt := newYouTube(); yt != nil {
		yt.OnMessage = func(msg chat.Message) { chat.Dispatch(handlers, msg, yt.Say) }
		platforms = append(platforms, platform{chat.YouTube, yt.Run, yt.Say})
	}
	if len(platforms) == 0 {
		log.Fatal("no platform configured: set CHAEBOT_TWITCH_USERNAME and/or CHAEBOT_YOUTUBE_CLIENT_ID (see .env.example)")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	sayers := map[chat.Platform]func(string){}
	for _, p := range platforms {
		sayers[p.name] = p.say
	}
	go sched.Run(ctx, sayers)
	if err := runAll(ctx, platforms); err != nil {
		log.Fatal(err)
	}
}

// platform is one connected chat.
type platform struct {
	name chat.Platform
	run  func(context.Context) error
	say  func(string)
}

// runAll runs every platform until ctx is cancelled or one fails, which
// stops the rest.
func runAll(ctx context.Context, platforms []platform) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	errs := make(chan error, len(platforms))
	for _, p := range platforms {
		go func() {
			err := p.run(ctx)
			if err != nil {
				err = fmt.Errorf("%s: %w", p.name, err)
				cancel()
			}
			errs <- err
		}()
	}
	var first error
	for range platforms {
		if err := <-errs; err != nil && first == nil {
			first = err
		}
	}
	return first
}

func newTwitch(tokenPath string) (*twitch.Client, error) {
	token, refresh, err := twitchAuth(tokenPath)
	if err != nil {
		return nil, err
	}
	return &twitch.Client{
		Username: os.Getenv("CHAEBOT_TWITCH_USERNAME"),
		Token:    token,
		Channel:  requireEnv("CHAEBOT_TWITCH_CHANNEL"),
		Refresh:  refresh,
	}, nil
}

// newYouTube returns a YouTube client if CHAEBOT_YOUTUBE_CLIENT_ID is set.
func newYouTube() *youtube.Client {
	if os.Getenv("CHAEBOT_YOUTUBE_CLIENT_ID") == "" {
		return nil
	}
	c := &youtube.Client{
		ClientID:     os.Getenv("CHAEBOT_YOUTUBE_CLIENT_ID"),
		ClientSecret: requireEnv("CHAEBOT_YOUTUBE_CLIENT_SECRET"),
		RefreshToken: requireEnv("CHAEBOT_YOUTUBE_REFRESH_TOKEN"),
	}
	if v := os.Getenv("CHAEBOT_YOUTUBE_POLL_SECONDS"); v != "" {
		seconds, err := strconv.Atoi(v)
		if err != nil || seconds < 1 {
			log.Fatalf("CHAEBOT_YOUTUBE_POLL_SECONDS must be a whole number of seconds, got %q", v)
		}
		c.PollInterval = time.Duration(seconds) * time.Second
	}
	return c
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
