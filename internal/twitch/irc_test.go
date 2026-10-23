package twitch

import (
	"testing"

	"chaebot/internal/chat"
)

func TestParsePrivmsg(t *testing.T) {
	tests := []struct {
		line string
		want chat.Message
	}{
		{
			"@badge-info=;badges=moderator/1;display-name=SomeMod;mod=1;user-id=1 :somemod!somemod@somemod.tmi.twitch.tv PRIVMSG #chablades :$addcommand $discord hi :)",
			chat.Message{Platform: chat.Twitch, Channel: "chablades", User: "SomeMod", IsMod: true, Text: "$addcommand $discord hi :)"},
		},
		{
			"@badges=broadcaster/1;display-name=Chablades;mod=0 :chablades!chablades@chablades.tmi.twitch.tv PRIVMSG #chablades :hello",
			chat.Message{Platform: chat.Twitch, Channel: "chablades", User: "Chablades", IsMod: true, Text: "hello"},
		},
		{
			"@badges=;display-name=;mod=0 :viewer!viewer@viewer.tmi.twitch.tv PRIVMSG #chablades :$discord",
			chat.Message{Platform: chat.Twitch, Channel: "chablades", User: "viewer", IsMod: false, Text: "$discord"},
		},
		{ // /me message
			"@badges=;display-name=Viewer;mod=0 :viewer!viewer@viewer.tmi.twitch.tv PRIVMSG #chablades :\x01ACTION $discord\x01",
			chat.Message{Platform: chat.Twitch, Channel: "chablades", User: "Viewer", IsMod: false, Text: "$discord"},
		},
	}
	for _, tt := range tests {
		l := parseLine(tt.line)
		if l.command != "PRIVMSG" {
			t.Fatalf("command = %q", l.command)
		}
		if got := l.message(); got != tt.want {
			t.Errorf("got %+v\nwant %+v", got, tt.want)
		}
	}
}

func TestParsePing(t *testing.T) {
	l := parseLine("PING :tmi.twitch.tv")
	if l.command != "PING" || l.trailing != "tmi.twitch.tv" {
		t.Errorf("got %+v", l)
	}
}

func TestUnescapeTag(t *testing.T) {
	for in, want := range map[string]string{
		"plain":       "plain",
		`a\sb`:        "a b",
		`x\:y`:        "x;y",
		`back\\slash`: `back\slash`,
		`cr\rlf\n`:    "cr\rlf\n",
		`unknown\q`:   "unknownq",
		`trailing\`:   "trailing",
	} {
		if got := unescapeTag(in); got != want {
			t.Errorf("unescapeTag(%q) = %q, want %q", in, got, want)
		}
	}
}
