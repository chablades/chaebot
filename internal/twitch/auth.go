package twitch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const tokenURL = "https://id.twitch.tv/oauth2/token"

// ErrRefreshDenied means Twitch rejected the refresh token or app
// credentials, so retrying won't help.
var ErrRefreshDenied = errors.New("twitch: token refresh denied (check the client ID, secret and refresh token)")

// Refresher gets new user access tokens with the OAuth refresh token flow.
// It needs a Twitch app you registered; ClientSecret may be empty for a
// public client.
type Refresher struct {
	ClientID     string
	ClientSecret string
	RefreshToken string

	// OnRefresh, if set, is called with each new token pair so it can be
	// saved; Twitch may rotate the refresh token.
	OnRefresh func(access, refresh string)

	url string // tokenURL if empty; set by tests
}

// Refresh fetches a new access token. It is not safe for concurrent use.
func (r *Refresher) Refresh(ctx context.Context) (string, error) {
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {r.RefreshToken},
		"client_id":     {r.ClientID},
	}
	if r.ClientSecret != "" {
		form.Set("client_secret", r.ClientSecret)
	}
	u := r.url
	if u == "" {
		u = tokenURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("twitch: token refresh: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return "", fmt.Errorf("twitch: token refresh: %w", err)
	}
	switch {
	case resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusUnauthorized:
		return "", fmt.Errorf("%w: %s", ErrRefreshDenied, body)
	case resp.StatusCode != http.StatusOK:
		return "", fmt.Errorf("twitch: token refresh: %s: %s", resp.Status, body)
	}
	var tok struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.Unmarshal(body, &tok); err != nil {
		return "", fmt.Errorf("twitch: token refresh: %w", err)
	}
	if tok.AccessToken == "" {
		return "", errors.New("twitch: token refresh: response has no access token")
	}
	if tok.RefreshToken != "" {
		r.RefreshToken = tok.RefreshToken
	}
	if r.OnRefresh != nil {
		r.OnRefresh(tok.AccessToken, r.RefreshToken)
	}
	return tok.AccessToken, nil
}
