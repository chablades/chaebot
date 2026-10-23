// Package chat holds the platform-neutral types shared by every chat
// listener (Twitch, YouTube) and every feature handler.
package chat

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Prefix starts every bot command, e.g. "$discord". It avoids "!" so
// Chaebot doesn't collide with Nightbot/StreamElements-style bots.
const Prefix = "$"

type Platform string

const (
	Twitch  Platform = "twitch"
	YouTube Platform = "youtube"
)

// Message is one incoming chat line, normalized across platforms.
type Message struct {
	Platform Platform
	Channel  string
	User     string // display name
	IsMod    bool   // moderator or broadcaster
	Text     string
}

// Handler processes a chat message. reply posts back to the same chat the
// message came from, so responses stay platform-native. Handle reports
// whether it consumed the message; later handlers are skipped if so.
type Handler interface {
	Handle(msg Message, reply func(text string)) bool
}

// Dispatch offers msg to each handler in order until one consumes it.
func Dispatch(handlers []Handler, msg Message, reply func(text string)) {
	for _, h := range handlers {
		if h.Handle(msg, reply) {
			return
		}
	}
}

// MaxLen is the longest text mods may save for the bot to post: Twitch's
// per-message limit. Platforms with a lower limit truncate when sending.
const MaxLen = 500

// CheckText returns a chat-ready description of why text can't be saved as
// something the bot posts, or "" if it's acceptable.
func CheckText(text string) string {
	// Twitch treats lines starting with / or . as chat commands, so a
	// response like "/ban x" would run with the bot's permissions.
	if strings.HasPrefix(text, "/") || strings.HasPrefix(text, ".") {
		return "Responses can't start with / or ."
	}
	if utf8.RuneCountInString(text) > MaxLen {
		return fmt.Sprintf("Response is too long (max %d characters).", MaxLen)
	}
	return ""
}

// CutWord splits s into its first whitespace-delimited word and the
// trimmed remainder.
func CutWord(s string) (word, rest string) {
	s = strings.TrimSpace(s)
	if i := strings.IndexFunc(s, unicode.IsSpace); i >= 0 {
		return s[:i], strings.TrimSpace(s[i:])
	}
	return s, ""
}

// Truncate shortens s to at most n characters.
func Truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}
