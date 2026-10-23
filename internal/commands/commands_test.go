package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"chaebot/internal/chat"
)

func newHandler(t *testing.T) (*Handler, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "commands.json")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	h := NewHandler(store, "$addtimer")
	// Each message arrives an hour after the last, so cooldowns never
	// interfere unless a test sets its own clock.
	now := time.Now()
	h.now = func() time.Time { now = now.Add(time.Hour); return now }
	return h, path
}

// say sends text as the given user and returns the bot's replies and
// whether the handler consumed the message.
func say(h *Handler, mod bool, text string) ([]string, bool) {
	return sayOn(h, chat.Twitch, mod, text)
}

func sayOn(h *Handler, p chat.Platform, mod bool, text string) ([]string, bool) {
	var replies []string
	msg := chat.Message{Platform: p, User: "cha", IsMod: mod, Text: text}
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
		{false, "$discord", ""},
		{true, "$addcommand $discord Join us at example.gg", "added"},
		{false, "$discord", "Join us at example.gg"},
		{false, "$DISCORD extra words", "Join us at example.gg"},
		{false, "!discord", ""},
		{true, "$addcommand $discord again", "already exists"},
		{true, "$editcommand $discord New link", "updated"},
		{false, "$discord", "New link"},
		{true, "$editcommand $nope x", "doesn't exist"},
		{true, "$delcommand $discord", "deleted"},
		{false, "$discord", ""},
		{true, "$delcommand $discord", "doesn't exist"},
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
		"$addcommand $x hi",
		"$editcommand $x hi",
		"$delcommand $x",
	} {
		replies, consumed := say(h, false, text)
		if len(replies) != 0 || !consumed {
			t.Errorf("%q from viewer: replies=%q consumed=%v", text, replies, consumed)
		}
	}
	if _, ok := h.store.Get("$x"); ok {
		t.Error("viewer was able to create a command")
	}
}

func TestValidation(t *testing.T) {
	h, _ := newHandler(t)
	for text, want := range map[string]string{
		"$addcommand":                                "Usage",
		"$addcommand $x":                             "Usage",
		"$addcommand discord hi":                     "must start with $",
		"$addcommand $ hi":                           "must start with $",
		"$addcommand $addcommand hi":                 "built-in",
		"$addcommand $commands hi":                   "built-in",
		"$addcommand $addtimer hi":                   "built-in",
		"$cooldown $x":                               "Usage",
		"$cooldown $x -1":                            "Usage",
		"$cooldown $x 3601":                          "Usage",
		"$cooldown $x 10":                            "doesn't exist",
		"$addcommand $x /ban someone":                "can't start with",
		"$addcommand $x .timeout someone":            "can't start with",
		"$addcommand $x " + strings.Repeat("a", 501): "too long",
		"$delcommand":                                "Usage",
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
	for _, text := range []string{"hello chat", "$uptime", ""} {
		if _, consumed := say(h, false, text); consumed {
			t.Errorf("%q should not be consumed", text)
		}
	}
}

func TestPersistence(t *testing.T) {
	h, path := newHandler(t)
	say(h, true, "$addcommand $discord Join   us  (spacing kept)")
	say(h, true, "$addcommand $gone bye")
	say(h, true, "$delcommand $gone")

	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := reopened.Get("$discord"); got.Response != "Join   us  (spacing kept)" {
		t.Errorf("reloaded response = %q", got.Response)
	}
	if _, ok := reopened.Get("$gone"); ok {
		t.Error("deleted command came back after reload")
	}
}

func TestVariables(t *testing.T) {
	h, _ := newHandler(t)
	say(h, true, "$addcommand $hug {user} hugs {touser}!")
	say(h, true, "$addcommand $echo You said: {args}")
	say(h, true, "$addcommand $deaths Deaths: {count}")
	for _, tt := range []struct{ text, want string }{
		{"$hug @Friend", "cha hugs Friend!"},
		{"$hug Friend and more", "cha hugs Friend!"},
		{"$hug", "cha hugs cha!"},
		{"$echo  spaced   out ", "You said: spaced   out"},
		{"$echo", "You said: "},
		{"$deaths", "Deaths: 1"},
		{"$deaths", "Deaths: 2"},
	} {
		replies, _ := say(h, false, tt.text)
		if len(replies) != 1 || replies[0] != tt.want {
			t.Errorf("%q: got %q, want %q", tt.text, replies, tt.want)
		}
	}
	// Counts survive edits.
	say(h, true, "$editcommand $deaths Died {count} times")
	if replies, _ := say(h, false, "$deaths"); len(replies) != 1 || replies[0] != "Died 3 times" {
		t.Errorf("after edit: %q", replies)
	}
}

func TestCooldown(t *testing.T) {
	h, _ := newHandler(t)
	now := time.Now()
	h.now = func() time.Time { return now }
	say(h, true, "$addcommand $discord link")

	uses := func(p chat.Platform, mod bool) int {
		replies, consumed := sayOn(h, p, mod, "$discord")
		if !consumed {
			t.Error("trigger on cooldown was not consumed")
		}
		return len(replies)
	}
	if uses(chat.Twitch, false) != 1 {
		t.Fatal("first use should reply")
	}
	if uses(chat.Twitch, false) != 0 {
		t.Error("second use within cooldown replied")
	}
	if uses(chat.YouTube, false) != 1 {
		t.Error("cooldown should be per platform")
	}
	if uses(chat.Twitch, true) != 1 {
		t.Error("mods should skip the cooldown")
	}
	now = now.Add(DefaultCooldown * time.Second)
	if uses(chat.Twitch, false) != 1 {
		t.Error("use after cooldown didn't reply")
	}

	if r, _ := say(h, true, "$cooldown $discord 0"); len(r) != 1 || !strings.Contains(r[0], "0s cooldown") {
		t.Fatalf("$cooldown reply: %q", r)
	}
	if uses(chat.Twitch, false) != 1 || uses(chat.Twitch, false) != 1 {
		t.Error("cooldown 0 should allow back-to-back uses")
	}
}

func TestList(t *testing.T) {
	h, _ := newHandler(t)
	if r, _ := say(h, false, "$commands"); len(r) != 1 || r[0] != "No custom commands yet." {
		t.Errorf("empty list: %q", r)
	}
	say(h, true, "$addcommand $zeta z")
	say(h, true, "$addcommand $alpha a")
	if r, _ := say(h, false, "$commands"); len(r) != 1 || r[0] != "Commands: $alpha, $zeta" {
		t.Errorf("list: %q", r)
	}
	for i := range 100 {
		say(h, true, "$addcommand $longcommandname"+strings.Repeat("x", i%10)+string(rune('a'+i%26))+string(rune('a'+i/26))+" r")
	}
	if r, _ := say(h, false, "$commands"); len(r) != 1 || len([]rune(r[0])) != chat.MaxLen || !strings.HasSuffix(r[0], "…") {
		t.Errorf("long list not truncated to %d: %d chars", chat.MaxLen, len([]rune(r[0])))
	}
}

func TestOpenOldFormat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "commands.json")
	os.WriteFile(path, []byte(`{"$discord": "Join us"}`), 0o644)
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	want := Command{Response: "Join us", Cooldown: DefaultCooldown}
	if got, _ := s.Get("$discord"); got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}
