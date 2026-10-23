// Package youtube connects to the live chat of the authorized channel's
// current YouTube stream and adapts it to the platform-neutral chat package.
package youtube

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"chaebot/internal/chat"
)

const apiURL = "https://www.googleapis.com/youtube/v3"

// maxLen is YouTube's live chat message limit.
const maxLen = 200

const (
	DefaultPollInterval = 15 * time.Second
	discoverInterval    = 2 * time.Minute
	quotaWait           = 30 * time.Minute
	// echoWindow is how long the bot remembers what it sent, to skip its
	// own messages when they come back in the chat feed.
	echoWindow = 2 * time.Minute
)

// Client listens to the live chat of whatever stream the authorized
// channel has live, checking for one every two minutes while offline. The
// bot posts as that channel.
type Client struct {
	ClientID     string
	ClientSecret string
	RefreshToken string // OAuth refresh token with the youtube scope
	OnMessage    func(chat.Message)
	// PollInterval is the shortest wait between chat reads; YouTube may ask
	// for longer. Each read costs API quota. Zero means DefaultPollInterval.
	PollInterval time.Duration

	apiURL, tokenURL string        // set by tests
	discover         time.Duration // discoverInterval if zero; set by tests

	tokMu  sync.Mutex
	access string
	expiry time.Time

	mu     sync.Mutex
	chatID string
	out    chan string
	sent   map[string]time.Time // recently sent text, and message IDs
}

// Run follows the channel's live chats until ctx is cancelled. It returns
// early only if the OAuth credentials are rejected.
func (c *Client) Run(ctx context.Context) error {
	c.mu.Lock()
	c.out = make(chan string, 10)
	c.sent = map[string]time.Time{}
	c.mu.Unlock()
	go c.sendLoop(ctx)

	for {
		chatID, err := c.findChat(ctx)
		wait := c.discover
		if wait == 0 {
			wait = discoverInterval
		}
		if err == nil && chatID != "" {
			log.Printf("youtube: connected to live chat")
			c.setChat(chatID)
			err = c.listen(ctx, chatID)
			c.setChat("")
			if err == nil && ctx.Err() == nil {
				log.Printf("youtube: live chat ended")
			}
		}
		if ctx.Err() != nil {
			return nil
		}
		if errors.Is(err, ErrRefreshDenied) {
			return err
		}
		if err != nil {
			var apiErr *apiError
			if errors.As(err, &apiErr) && apiErr.Reason == "quotaExceeded" {
				wait = quotaWait
			}
			log.Printf("youtube: %v; retrying in %s", err, wait)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(wait):
		}
	}
}

// Say posts text to the current live chat. It drops the message if no
// stream is live or too many messages are waiting to send.
func (c *Client) Say(text string) {
	text = chat.Truncate(strings.TrimSpace(text), maxLen)
	if text == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.chatID == "" {
		return
	}
	select {
	case c.out <- text:
	default:
		log.Printf("youtube: send queue full, dropping message %q", text)
	}
}

func (c *Client) setChat(id string) {
	c.mu.Lock()
	c.chatID = id
	c.mu.Unlock()
}

func (c *Client) sendLoop(ctx context.Context) {
	for {
		var text string
		select {
		case <-ctx.Done():
			return
		case text = <-c.out:
		}
		c.mu.Lock()
		chatID := c.chatID
		c.remember(text)
		c.mu.Unlock()
		if chatID == "" {
			continue
		}
		var req struct {
			Snippet struct {
				LiveChatID         string `json:"liveChatId"`
				Type               string `json:"type"`
				TextMessageDetails struct {
					MessageText string `json:"messageText"`
				} `json:"textMessageDetails"`
			} `json:"snippet"`
		}
		req.Snippet.LiveChatID = chatID
		req.Snippet.Type = "textMessageEvent"
		req.Snippet.TextMessageDetails.MessageText = text
		var resp struct {
			ID string `json:"id"`
		}
		err := c.do(ctx, http.MethodPost, "/liveChat/messages", url.Values{"part": {"snippet"}}, req, &resp)
		if err != nil {
			if ctx.Err() == nil {
				log.Printf("youtube: send failed: %v", err)
			}
			continue
		}
		c.mu.Lock()
		c.remember("id:" + resp.ID)
		c.mu.Unlock()
	}
}

// remember records something the bot sent and forgets old entries;
// callers must hold c.mu.
func (c *Client) remember(key string) {
	now := time.Now()
	for k, t := range c.sent {
		if now.Sub(t) > echoWindow {
			delete(c.sent, k)
		}
	}
	c.sent[key] = now
}

// isEcho reports whether a chat message is one the bot sent. The bot posts
// as the channel owner, so the owner's messages are only skipped when they
// match, letting the streamer still use commands themselves.
func (c *Client) isEcho(m liveChatMessage) bool {
	if !m.AuthorDetails.IsChatOwner {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	_, byID := c.sent["id:"+m.ID]
	_, byText := c.sent[strings.TrimSpace(m.Snippet.DisplayMessage)]
	return byID || byText
}

// findChat returns the live chat ID of the authorized channel's active
// broadcast, or "" if it isn't live.
func (c *Client) findChat(ctx context.Context) (string, error) {
	var resp struct {
		Items []struct {
			Snippet struct {
				LiveChatID string `json:"liveChatId"`
			} `json:"snippet"`
		} `json:"items"`
	}
	q := url.Values{"part": {"snippet"}, "broadcastStatus": {"active"}, "broadcastType": {"all"}}
	if err := c.do(ctx, http.MethodGet, "/liveBroadcasts", q, nil, &resp); err != nil {
		return "", err
	}
	for _, b := range resp.Items {
		if b.Snippet.LiveChatID != "" {
			return b.Snippet.LiveChatID, nil
		}
	}
	return "", nil
}

type liveChatMessage struct {
	ID      string `json:"id"`
	Snippet struct {
		Type           string `json:"type"`
		DisplayMessage string `json:"displayMessage"`
	} `json:"snippet"`
	AuthorDetails struct {
		ChannelID       string `json:"channelId"`
		DisplayName     string `json:"displayName"`
		IsChatOwner     bool   `json:"isChatOwner"`
		IsChatModerator bool   `json:"isChatModerator"`
	} `json:"authorDetails"`
}

type messagePage struct {
	NextPageToken         string            `json:"nextPageToken"`
	PollingIntervalMillis int               `json:"pollingIntervalMillis"`
	OfflineAt             string            `json:"offlineAt"`
	Items                 []liveChatMessage `json:"items"`
}

// listen reads chatID until the chat ends (returning nil) or a request
// fails.
func (c *Client) listen(ctx context.Context, chatID string) error {
	minWait := c.PollInterval
	if minWait <= 0 {
		minWait = DefaultPollInterval
	}
	// The first page is recent history; skip it so old commands don't run.
	page, err := c.messages(ctx, chatID, "")
	for ; err == nil; page, err = c.messages(ctx, chatID, page.NextPageToken) {
		if page.OfflineAt != "" {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(max(minWait, time.Duration(page.PollingIntervalMillis)*time.Millisecond)):
		}
	}
	var apiErr *apiError
	if errors.As(err, &apiErr) {
		switch apiErr.Reason {
		case "liveChatEnded", "liveChatNotFound", "liveChatDisabled":
			return nil
		}
	}
	return err
}

// messages fetches one page of chat after pageToken and dispatches it,
// unless pageToken is "" (the first page).
func (c *Client) messages(ctx context.Context, chatID, pageToken string) (messagePage, error) {
	var page messagePage
	q := url.Values{"liveChatId": {chatID}, "part": {"snippet,authorDetails"}}
	if pageToken != "" {
		q.Set("pageToken", pageToken)
	}
	if err := c.do(ctx, http.MethodGet, "/liveChat/messages", q, nil, &page); err != nil {
		return page, err
	}
	if pageToken == "" || c.OnMessage == nil {
		return page, nil
	}
	for _, m := range page.Items {
		if m.Snippet.Type != "textMessageEvent" || c.isEcho(m) {
			continue
		}
		c.OnMessage(chat.Message{
			Platform: chat.YouTube,
			Channel:  chatID,
			User:     m.AuthorDetails.DisplayName,
			IsMod:    m.AuthorDetails.IsChatOwner || m.AuthorDetails.IsChatModerator,
			Text:     m.Snippet.DisplayMessage,
		})
	}
	return page, nil
}

// apiError is a YouTube API error response.
type apiError struct {
	Code    int
	Reason  string // e.g. "quotaExceeded", "liveChatEnded"
	Message string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("youtube: API error %d %s: %s", e.Code, e.Reason, e.Message)
}

// do calls the API, retrying once with a fresh token if the access token is
// rejected, and decodes the JSON response into out.
func (c *Client) do(ctx context.Context, method, path string, q url.Values, body, out any) error {
	base := c.apiURL
	if base == "" {
		base = apiURL
	}
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return err
		}
	}
	for attempt := 0; ; attempt++ {
		token, err := c.token(ctx, attempt > 0)
		if err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(ctx, method, base+path+"?"+q.Encode(), bytes.NewReader(payload))
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		resp.Body.Close()
		if err != nil {
			return err
		}
		if resp.StatusCode == http.StatusUnauthorized && attempt == 0 {
			continue
		}
		if resp.StatusCode >= 300 {
			var e struct {
				Error struct {
					Message string `json:"message"`
					Errors  []struct {
						Reason string `json:"reason"`
					} `json:"errors"`
				} `json:"error"`
			}
			json.Unmarshal(data, &e)
			apiErr := &apiError{Code: resp.StatusCode, Message: e.Error.Message}
			if len(e.Error.Errors) > 0 {
				apiErr.Reason = e.Error.Errors[0].Reason
			}
			return apiErr
		}
		return json.Unmarshal(data, out)
	}
}
