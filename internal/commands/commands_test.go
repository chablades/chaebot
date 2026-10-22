package commands

import (
	"path/filepath"
	"strings"
	"testing"

	"chaebot/internal/chat"
)

func newHandler(t *testing.T) (*Handler, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "commands.json")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	return NewHandler(store), path
}

// say sends text as the given user and returns the bot's replies and
// whether the handler consumed the message.
func say(h *Handler, mod bool, text string) ([]string, bool) {
	var replies []string
	msg := chat.Message{Platform: chat.Twitch, User: "cha", IsMod: mod, Text: text}
	ok := h.Handle(msg, func(s string) { replies = append(replies, s) })
	return replies, ok
}

func TestLifecycle(t *testing.T) {
	h, _ := newHandler(t)

	steps := []struct {
		mod  bool
		text string
		want string // substring of the single expected reply; "" = no reply
	}{
		{false, "!discord", ""},
		{true, "!addcommand !discord Join us at example.gg", "added"},
		{false, "!discord", "Join us at example.gg"},
		{false, "!DISCORD extra words", "Join us at example.gg"},
		{true, "!addcommand !discord again", "already exists"},
		{true, "!editcommand !discord New link", "updated"},
		{false, "!discord", "New link"},
		{true, "!editcommand !nope x", "doesn't exist"},
		{true, "!delcommand !discord", "deleted"},
		{false, "!discord", ""},
		{true, "!delcommand !discord", "doesn't exist"},
	}
	for _, s := range steps {
		replies, _ := say(h, s.mod, s.text)
		if s.want == "" {
			if len(replies) != 0 {
				t.Fatalf("%q: want no reply, got %q", s.text, replies)
			}
			continue
		}
		if len(replies) != 1 || !strings.Contains(replies[0], s.want) {
			t.Fatalf("%q: want reply containing %q, got %q", s.text, s.want, replies)
		}
	}
}

func TestNonModCannotManage(t *testing.T) {
	h, _ := newHandler(t)
	for _, text := range []string{
		"!addcommand !x hi",
		"!editcommand !x hi",
		"!delcommand !x",
	} {
		replies, consumed := say(h, false, text)
		if len(replies) != 0 || !consumed {
			t.Errorf("%q from viewer: replies=%q consumed=%v", text, replies, consumed)
		}
	}
	if _, ok := h.store.Get("!x"); ok {
		t.Error("viewer was able to create a command")
	}
}

func TestValidation(t *testing.T) {
	h, _ := newHandler(t)
	for text, want := range map[string]string{
		"!addcommand":                                "Usage",
		"!addcommand !x":                             "Usage",
		"!addcommand discord hi":                     "must start with !",
		"!addcommand ! hi":                           "must start with !",
		"!addcommand !addcommand hi":                 "built-in",
		"!addcommand !x /ban someone":                "can't start with",
		"!addcommand !x .timeout someone":            "can't start with",
		"!addcommand !x " + strings.Repeat("a", 501): "too long",
		"!delcommand":                                "Usage",
	} {
		replies, _ := say(h, true, text)
		if len(replies) != 1 || !strings.Contains(replies[0], want) {
			t.Errorf("%.40q: want reply containing %q, got %q", text, want, replies)
		}
	}
	if n := len(h.store.Triggers()); n != 0 {
		t.Errorf("invalid commands were saved: %d", n)
	}
}

func TestUnknownMessagesPassThrough(t *testing.T) {
	h, _ := newHandler(t)
	for _, text := range []string{"hello chat", "!uptime", ""} {
		if _, consumed := say(h, false, text); consumed {
			t.Errorf("%q should not be consumed", text)
		}
	}
}

func TestPersistence(t *testing.T) {
	h, path := newHandler(t)
	say(h, true, "!addcommand !discord Join   us  (spacing kept)")
	say(h, true, "!addcommand !gone bye")
	say(h, true, "!delcommand !gone")

	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := reopened.Get("!discord"); got != "Join   us  (spacing kept)" {
		t.Errorf("reloaded response = %q", got)
	}
	if _, ok := reopened.Get("!gone"); ok {
		t.Error("deleted command came back after reload")
	}
}
