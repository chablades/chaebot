package youtube

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"chaebot/internal/chat"
)

func msg(id, author, text string, owner, mod bool) map[string]any {
	return map[string]any{
		"id":            id,
		"snippet":       map[string]any{"type": "textMessageEvent", "displayMessage": text},
		"authorDetails": map[string]any{"channelId": "UC" + author, "displayName": author, "isChatOwner": owner, "isChatModerator": mod},
	}
}

// fakeGoogle serves the token endpoint and the YouTube API. Only the second
// access token it issues is accepted, so every client must refresh once.
type fakeGoogle struct {
	t        *testing.T
	mu       sync.Mutex
	tokens   int
	inserted chan string
	pages    map[string]map[string]any // by pageToken
}

func (f *fakeGoogle) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.URL.Path == "/token" {
		r.ParseForm()
		if r.PostForm.Get("refresh_token") != "refresh" || r.PostForm.Get("client_secret") != "secret" {
			http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
			return
		}
		f.tokens++
		json.NewEncoder(w).Encode(map[string]any{"access_token": fmt.Sprint("tok", f.tokens), "expires_in": 3600})
		return
	}
	if r.Header.Get("Authorization") != "Bearer tok2" {
		http.Error(w, `{"error":{"code":401,"message":"bad token"}}`, http.StatusUnauthorized)
		return
	}
	switch {
	case r.URL.Path == "/liveBroadcasts":
		if r.URL.Query().Get("broadcastStatus") != "active" {
			f.t.Errorf("liveBroadcasts query = %v", r.URL.Query())
		}
		json.NewEncoder(w).Encode(map[string]any{"items": []any{map[string]any{"snippet": map[string]any{"liveChatId": "chat1"}}}})
	case r.URL.Path == "/liveChat/messages" && r.Method == http.MethodGet:
		if r.URL.Query().Get("liveChatId") != "chat1" {
			f.t.Errorf("messages for chat %q", r.URL.Query().Get("liveChatId"))
		}
		token := r.URL.Query().Get("pageToken")
		if token == "p2" {
			// Wait for the bot's reply so its echo comes back in this page.
			f.mu.Unlock()
			select {
			case <-f.inserted:
			case <-time.After(5 * time.Second):
				f.t.Error("bot never replied")
			}
			f.mu.Lock()
		}
		page, ok := f.pages[token]
		if !ok {
			http.Error(w, `{"error":{"code":403,"message":"ended","errors":[{"reason":"liveChatEnded"}]}}`, http.StatusForbidden)
			return
		}
		json.NewEncoder(w).Encode(page)
	case r.URL.Path == "/liveChat/messages" && r.Method == http.MethodPost:
		var body struct {
			Snippet struct {
				LiveChatID         string
				Type               string
				TextMessageDetails struct{ MessageText string }
			}
		}
		json.NewDecoder(r.Body).Decode(&body)
		if body.Snippet.LiveChatID != "chat1" || body.Snippet.Type != "textMessageEvent" {
			f.t.Errorf("insert body = %+v", body)
		}
		json.NewEncoder(w).Encode(map[string]any{"id": "sent1"})
		f.inserted <- body.Snippet.TextMessageDetails.MessageText
	default:
		f.t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		http.NotFound(w, r)
	}
}

func TestLiveChat(t *testing.T) {
	fake := &fakeGoogle{t: t, inserted: make(chan string, 10), pages: map[string]map[string]any{
		"": {"nextPageToken": "p1", "items": []any{msg("m0", "Viewer", "$old", false, false)}},
		"p1": {"nextPageToken": "p2", "items": []any{
			msg("m1", "Viewer", "$ping", false, false),
			msg("m2", "Mod", "$addcommand $x y", false, true),
			map[string]any{"id": "m3", "snippet": map[string]any{"type": "superChatEvent"}},
		}},
		"p2": {"nextPageToken": "p3", "items": []any{
			msg("sent1", "Owner", "pong!", true, false), // the bot's own reply
			msg("m4", "Owner", "$discord", true, false), // the streamer typing
		}},
		"p3": {"offlineAt": "2026-10-23T00:00:00Z"},
	}}
	srv := httptest.NewServer(fake)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var mu sync.Mutex
	var got []chat.Message
	c := &Client{
		ClientID: "id", ClientSecret: "secret", RefreshToken: "refresh",
		PollInterval: time.Millisecond, discover: time.Hour,
		apiURL: srv.URL, tokenURL: srv.URL + "/token",
	}
	c.OnMessage = func(m chat.Message) {
		mu.Lock()
		got = append(got, m)
		mu.Unlock()
		if m.Text == "$ping" {
			c.Say("  pong!  ")
		}
		if m.Text == "$discord" {
			cancel()
		}
	}
	if err := c.Run(ctx); err != nil {
		t.Fatal(err)
	}

	want := []chat.Message{
		{Platform: chat.YouTube, Channel: "chat1", User: "Viewer", IsMod: false, Text: "$ping"},
		{Platform: chat.YouTube, Channel: "chat1", User: "Mod", IsMod: true, Text: "$addcommand $x y"},
		{Platform: chat.YouTube, Channel: "chat1", User: "Owner", IsMod: true, Text: "$discord"},
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != len(want) {
		t.Fatalf("got %d messages %+v, want %+v", len(got), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("message %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestRefreshDeniedIsFatal(t *testing.T) {
	srv := httptest.NewServer(&fakeGoogle{t: t})
	defer srv.Close()
	c := &Client{ClientID: "id", ClientSecret: "wrong", RefreshToken: "refresh", apiURL: srv.URL, tokenURL: srv.URL + "/token"}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Run(ctx); !errors.Is(err, ErrRefreshDenied) {
		t.Fatalf("Run = %v, want ErrRefreshDenied", err)
	}
}

func TestSayWhileOffline(t *testing.T) {
	c := &Client{out: make(chan string, 1)}
	c.Say("hello")
	if len(c.out) != 0 {
		t.Error("queued a message with no live chat")
	}
	c.chatID = "chat1"
	c.Say(strings.Repeat("a", 300))
	if got := <-c.out; len([]rune(got)) > maxLen {
		t.Errorf("message not truncated: %d characters", len([]rune(got)))
	}
}
