// Package twitch connects to Twitch chat over IRC and adapts it to the
// platform-neutral chat package.
package twitch

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"chaebot/internal/chat"
)

const ircAddr = "irc.chat.twitch.tv:6697"

// Twitch allows 20 messages per 30 seconds for accounts that aren't
// moderators; exceeding it locks the bot out of chat for a while.
const (
	rateLimit  = 20
	rateWindow = 30 * time.Second
)

var errAuth = errors.New("twitch: login authentication failed (check the OAuth token)")

// Client is a Twitch chat connection for a single channel.
type Client struct {
	Username  string // bot account login
	Token     string // OAuth token with chat:read and chat:edit scopes
	Channel   string // channel login to join, without '#'
	OnMessage func(chat.Message)

	mu   sync.Mutex
	conn net.Conn
	sent []time.Time // send times within the current rate window
}

// Run connects and processes chat until ctx is cancelled, reconnecting
// with backoff on connection loss. It returns early on auth failure.
func (c *Client) Run(ctx context.Context) error {
	backoff := time.Second
	for {
		start := time.Now()
		err := c.session(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if errors.Is(err, errAuth) {
			return err
		}
		if time.Since(start) > time.Minute {
			backoff = time.Second
		}
		log.Printf("twitch: disconnected (%v), reconnecting in %s", err, backoff)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 2*time.Minute)
	}
}

// Say posts text to the channel, dropping it if the rate limit is reached.
func (c *Client) Say(text string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return
	}
	now := time.Now()
	recent := c.sent[:0]
	for _, t := range c.sent {
		if now.Sub(t) < rateWindow {
			recent = append(recent, t)
		}
	}
	c.sent = recent
	if len(c.sent) >= rateLimit {
		log.Printf("twitch: rate limit reached, dropping message %q", text)
		return
	}
	c.sent = append(c.sent, now)
	// IRC lines end at a newline, so collapse any to keep the message whole.
	text = strings.NewReplacer("\r", " ", "\n", " ").Replace(text)
	if err := c.write("PRIVMSG #%s :%s", c.Channel, text); err != nil {
		log.Printf("twitch: send failed: %v", err)
	}
}

func (c *Client) session(ctx context.Context) error {
	conn, err := (&tls.Dialer{}).DialContext(ctx, "tcp", ircAddr)
	if err != nil {
		return err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()

	c.mu.Lock()
	c.conn = conn
	token := strings.TrimPrefix(c.Token, "oauth:")
	err = errors.Join(
		c.write("CAP REQ :twitch.tv/tags twitch.tv/commands"),
		c.write("PASS oauth:%s", token),
		c.write("NICK %s", strings.ToLower(c.Username)),
		c.write("JOIN #%s", strings.ToLower(c.Channel)),
	)
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.conn = nil
		c.mu.Unlock()
	}()
	if err != nil {
		return err
	}

	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 64*1024), 64*1024)
	for sc.Scan() {
		line := parseLine(sc.Text())
		switch line.command {
		case "PING":
			c.mu.Lock()
			err := c.write("PONG :%s", line.trailing)
			c.mu.Unlock()
			if err != nil {
				return err
			}
		case "RECONNECT":
			return errors.New("server requested reconnect")
		case "NOTICE":
			if strings.Contains(line.trailing, "authentication failed") ||
				strings.Contains(line.trailing, "Improperly formatted auth") {
				return errAuth
			}
		case "001":
			log.Printf("twitch: connected to #%s", c.Channel)
		case "PRIVMSG":
			if c.OnMessage != nil {
				c.OnMessage(line.message())
			}
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return errors.New("connection closed")
}

// write sends one IRC line; callers must hold c.mu.
func (c *Client) write(format string, args ...any) error {
	_, err := fmt.Fprintf(c.conn, format+"\r\n", args...)
	return err
}

// ircLine is a parsed IRCv3 line:
// [@tags] [:prefix] COMMAND [params...] [:trailing]
type ircLine struct {
	tags     map[string]string
	prefix   string
	command  string
	params   []string
	trailing string
}

func parseLine(s string) ircLine {
	var l ircLine
	if strings.HasPrefix(s, "@") {
		var raw string
		raw, s, _ = strings.Cut(s[1:], " ")
		l.tags = map[string]string{}
		for _, kv := range strings.Split(raw, ";") {
			k, v, _ := strings.Cut(kv, "=")
			l.tags[k] = v
		}
	}
	if strings.HasPrefix(s, ":") {
		l.prefix, s, _ = strings.Cut(s[1:], " ")
	}
	s, l.trailing, _ = strings.Cut(s, " :")
	fields := strings.Fields(s)
	if len(fields) > 0 {
		l.command, l.params = fields[0], fields[1:]
	}
	return l
}

func (l ircLine) message() chat.Message {
	nick, _, _ := strings.Cut(l.prefix, "!")
	user := l.tags["display-name"]
	if user == "" {
		user = nick
	}
	var channel string
	if len(l.params) > 0 {
		channel = strings.TrimPrefix(l.params[0], "#")
	}
	return chat.Message{
		Platform: chat.Twitch,
		Channel:  channel,
		User:     user,
		IsMod:    l.tags["mod"] == "1" || strings.Contains(l.tags["badges"], "broadcaster/"),
		Text:     l.trailing,
	}
}
