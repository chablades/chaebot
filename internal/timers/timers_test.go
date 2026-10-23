package timers

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"chaebot/internal/chat"
)

type fixture struct {
	t    *testing.T
	s    *Scheduler
	now  time.Time
	sent map[chat.Platform][]string
}

func newFixture(t *testing.T) *fixture {
	s, err := Open(filepath.Join(t.TempDir(), "timers.json"))
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{t: t, s: s, now: time.Now(), sent: map[chat.Platform][]string{}}
	s.now = func() time.Time { return f.now }
	return f
}

func (f *fixture) say(p chat.Platform, mod bool, text string) []string {
	var replies []string
	f.s.Handle(chat.Message{Platform: p, User: "cha", IsMod: mod, Text: text}, func(r string) {
		replies = append(replies, r)
	})
	return replies
}

func (f *fixture) chatter(p chat.Platform, n int) {
	for range n {
		f.say(p, false, "hello")
	}
}

// tick advances the clock by d and runs the scheduler for both platforms.
func (f *fixture) tick(d time.Duration) {
	f.now = f.now.Add(d)
	f.s.Tick(map[chat.Platform]func(string){
		chat.Twitch:  func(s string) { f.sent[chat.Twitch] = append(f.sent[chat.Twitch], s) },
		chat.YouTube: func(s string) { f.sent[chat.YouTube] = append(f.sent[chat.YouTube], s) },
	})
}

func TestTimerPostsWhenDueAndActive(t *testing.T) {
	f := newFixture(t)
	if r := f.say(chat.Twitch, true, "$addtimer socials 10 Follow my socials!"); len(r) != 1 || !strings.Contains(r[0], "added") {
		t.Fatalf("add: %q", r)
	}
	f.tick(0) // schedule starts
	f.chatter(chat.Twitch, MinLines)
	f.tick(9 * time.Minute)
	if len(f.sent[chat.Twitch]) != 0 {
		t.Fatal("posted before its interval")
	}
	f.tick(time.Minute)
	if got := f.sent[chat.Twitch]; len(got) != 1 || got[0] != "Follow my socials!" {
		t.Fatalf("Twitch got %q", got)
	}
	if len(f.sent[chat.YouTube]) != 0 {
		t.Error("posted to a platform with no chat activity")
	}
	f.tick(10 * time.Minute)
	if len(f.sent[chat.Twitch]) != 1 {
		t.Error("posted again without new chat activity")
	}
	f.chatter(chat.Twitch, MinLines)
	f.tick(tick)
	if len(f.sent[chat.Twitch]) != 2 {
		t.Error("didn't post once chat was active again")
	}
}

func TestOneTimerPerTick(t *testing.T) {
	f := newFixture(t)
	f.say(chat.Twitch, true, "$addtimer a 5 first")
	f.say(chat.Twitch, true, "$addtimer b 5 second")
	f.tick(0)
	f.chatter(chat.Twitch, MinLines)
	f.tick(5 * time.Minute)
	f.chatter(chat.Twitch, MinLines)
	f.tick(tick)
	if got := f.sent[chat.Twitch]; len(got) != 2 || got[0] == got[1] {
		t.Errorf("want both timers on separate ticks, got %q", got)
	}
}

func TestManagement(t *testing.T) {
	f := newFixture(t)
	for _, tt := range []struct {
		mod        bool
		text, want string
	}{
		{false, "$addtimer x 10 hi", ""},
		{true, "$timers", "No timers yet"},
		{true, "$addtimer x 4 hi", "Usage"},
		{true, "$addtimer x 1441 hi", "Usage"},
		{true, "$addtimer x ten hi", "Usage"},
		{true, "$addtimer x 10", "Usage"},
		{true, "$addtimer x 10 /ban y", "can't start with"},
		{true, "$addtimer X 10 hi", "added"},
		{true, "$addtimer x 20 hi", "already exists"},
		{true, "$timers", "Timers: x (10m)"},
		{true, "$deltimer x", "deleted"},
		{true, "$deltimer x", "doesn't exist"},
	} {
		r := f.say(chat.Twitch, tt.mod, tt.text)
		if tt.want == "" {
			if len(r) != 0 {
				t.Errorf("%q: want no reply, got %q", tt.text, r)
			}
			continue
		}
		if len(r) != 1 || !strings.Contains(r[0], tt.want) {
			t.Errorf("%q: want %q, got %q", tt.text, tt.want, r)
		}
	}
}

func TestPersistence(t *testing.T) {
	f := newFixture(t)
	f.say(chat.Twitch, true, "$addtimer socials 15 Follow me")
	s, err := Open(f.s.path)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.timers["socials"]; got != (Timer{Interval: 15, Message: "Follow me"}) {
		t.Errorf("reloaded %+v", got)
	}
}

func TestOtherMessagesPassThrough(t *testing.T) {
	f := newFixture(t)
	consumed := f.s.Handle(chat.Message{Platform: chat.Twitch, Text: "$discord"}, func(string) {})
	if consumed {
		t.Error("non-timer command was consumed")
	}
}
