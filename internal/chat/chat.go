// Package chat holds the platform-neutral types shared by every chat
// listener (Twitch, YouTube) and every feature handler.
package chat

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
