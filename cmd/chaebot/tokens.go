package main

import (
	"context"
	"errors"
	"log"
	"os"

	"chaebot/internal/jsonfile"
	"chaebot/internal/twitch"
)

// savedTokens is the latest Twitch token pair, kept across restarts because
// a refresh can rotate the refresh token in .env out of date. Seed is the
// .env refresh token it descends from, so pasting a new one in .env wins.
type savedTokens struct {
	Seed    string `json:"seed"`
	Access  string `json:"access"`
	Refresh string `json:"refresh"`
}

// twitchAuth reads the Twitch credentials from the environment. Token
// refresh is enabled when a client ID and refresh token are both set; then
// CHAEBOT_TWITCH_TOKEN is optional and newer tokens are kept in path.
func twitchAuth(path string) (token string, refresh func(context.Context) (string, error), err error) {
	token = os.Getenv("CHAEBOT_TWITCH_TOKEN")
	clientID := os.Getenv("CHAEBOT_TWITCH_CLIENT_ID")
	seed := os.Getenv("CHAEBOT_TWITCH_REFRESH_TOKEN")
	if (clientID == "") != (seed == "") {
		return "", nil, errors.New("set both CHAEBOT_TWITCH_CLIENT_ID and CHAEBOT_TWITCH_REFRESH_TOKEN to enable token refresh, or neither")
	}
	if clientID == "" {
		if token == "" {
			return "", nil, errors.New("CHAEBOT_TWITCH_TOKEN must be set")
		}
		return token, nil, nil
	}

	r := &twitch.Refresher{
		ClientID:     clientID,
		ClientSecret: os.Getenv("CHAEBOT_TWITCH_CLIENT_SECRET"),
		RefreshToken: seed,
		OnRefresh: func(access, refresh string) {
			if err := saveTokens(path, savedTokens{Seed: seed, Access: access, Refresh: refresh}); err != nil {
				log.Printf("twitch: couldn't save refreshed token: %v", err)
			}
		},
	}
	saved, err := loadTokens(path)
	if err != nil {
		return "", nil, err
	}
	if saved.Seed == seed {
		token, r.RefreshToken = saved.Access, saved.Refresh
	}
	// An empty token fails the first login, which triggers a refresh.
	return token, r.Refresh, nil
}

// loadTokens reads path, returning zero tokens if it doesn't exist.
func loadTokens(path string) (savedTokens, error) {
	var t savedTokens
	err := jsonfile.Load(path, &t)
	return t, err
}

// saveTokens writes path readable only by the current user.
func saveTokens(path string, t savedTokens) error {
	return jsonfile.Save(path, t, 0o600)
}
