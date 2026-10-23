package youtube

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const tokenURL = "https://oauth2.googleapis.com/token"

// ErrRefreshDenied means Google rejected the refresh token or client
// credentials, so retrying won't help.
var ErrRefreshDenied = errors.New("youtube: token refresh denied (check the client ID, secret and refresh token)")

// token returns a valid access token, refreshing it if it has expired or
// force is set.
func (c *Client) token(ctx context.Context, force bool) (string, error) {
	c.tokMu.Lock()
	defer c.tokMu.Unlock()
	if !force && c.access != "" && time.Now().Before(c.expiry) {
		return c.access, nil
	}
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {c.RefreshToken},
		"client_id":     {c.ClientID},
		"client_secret": {c.ClientSecret},
	}
	u := c.tokenURL
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
		return "", fmt.Errorf("youtube: token refresh: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return "", fmt.Errorf("youtube: token refresh: %w", err)
	}
	switch {
	case resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusUnauthorized:
		return "", fmt.Errorf("%w: %s", ErrRefreshDenied, body)
	case resp.StatusCode != http.StatusOK:
		return "", fmt.Errorf("youtube: token refresh: %s: %s", resp.Status, body)
	}
	var tok struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tok); err != nil {
		return "", fmt.Errorf("youtube: token refresh: %w", err)
	}
	if tok.AccessToken == "" {
		return "", errors.New("youtube: token refresh: response has no access token")
	}
	c.access = tok.AccessToken
	// Refresh a minute early so a token never expires mid-request.
	c.expiry = time.Now().Add(time.Duration(tok.ExpiresIn)*time.Second - time.Minute)
	return c.access, nil
}
