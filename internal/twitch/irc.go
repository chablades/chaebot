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

// Twitch allows 20 messages per 30 seconds, or 100 if the bot is a
// moderator or the broadcaster; exceeding it locks the bot out of chat for
// a while.
const (
	rateLimit    = 20
	modRateLimit = 100
	rateWindow   = 30 * time.Second
)

// Twitch pings about every five minutes, so a longer silence means the
// connection died without being closed. Variables so tests can shorten them.
var (
	readTimeout  = 6 * time.Minute
	writeTimeout = 10 * time.Second
)

var errAuth = errors.New("twitch: login authentication failed (check the OAuth token)")

// Client is a Twitch chat connection for a single channel.
type Client struct {
	Username  string // bot account login
	Token     string // OAuth token with chat:read and chat:edit scopes
	Channel   string // channel login to join, without '#'
	OnMessage func(chat.Message)

	// Refresh, if set, is called when Twitch rejects Token, and returns a
	// new one. If it fails with ErrRefreshDenied, Run gives up; other
	// errors are retried with backoff.
	Refresh func(ctx context.Context) (string, error)

	dial func(ctx context.Context) (net.Conn, error) // nil dials Twitch; set by tests

	mu    sync.Mutex
	conn  net.Conn
	isMod bool        // from USERSTATE; raises the rate limit
	sent  []time.Time // send times within the current rate window
}

// Run connects and processes chat until ctx is cancelled, reconnecting
// with backoff on connection loss. It returns early if the token is
// rejected and can't be refreshed.
func (c *Client) Run(ctx context.Context) error {
	backoff := time.Second
	refreshed := false // token refreshed since the last successful login
	for {
		start := time.Now()
		loggedIn, err := c.session(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if loggedIn {
			refreshed = false
		}
		if errors.Is(err, errAuth) {
			if c.Refresh == nil || refreshed {
				return err
			}
			log.Printf("twitch: token rejected, refreshing")
			token, rerr := c.Refresh(ctx)
			if errors.Is(rerr, ErrRefreshDenied) {
				return errors.Join(err, rerr)
			}
			if rerr == nil {
				c.mu.Lock()
				c.Token = token
				c.mu.Unlock()
				refreshed = true
				continue
			}
			err = rerr
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
	limit := rateLimit
	if c.isMod {
		limit = modRateLimit
	}
	if len(c.sent) >= limit {
		log.Printf("twitch: rate limit reached, dropping message %q", text)
		return
	}
	c.sent = append(c.sent, now)
	text = sanitize(text)
	if err := c.write("PRIVMSG #%s :%s", c.Channel, text); err != nil {
		log.Printf("twitch: send failed: %v", err)
		// A timed-out write leaves the connection unusable; closing it
		// makes the reader reconnect.
		c.conn.Close()
	}
}

// sanitize makes text safe to send as one chat message. Responses can
// include viewer text through variables like {args}, so this is the last
// line of defense against the bot running a chat command like "/ban".
func sanitize(text string) string {
	// IRC lines end at a newline, so collapse any to keep the message whole.
	text = strings.NewReplacer("\r", " ", "\n", " ").Replace(text)
	text = strings.TrimLeft(text, " \t/.")
	return chat.Truncate(text, chat.MaxLen)
}

// session runs one connection until it fails. loggedIn reports whether
// Twitch accepted the login before then.
func (c *Client) session(ctx context.Context) (loggedIn bool, err error) {
	dial := c.dial
	if dial == nil {
		dial = func(ctx context.Context) (net.Conn, error) {
			return (&tls.Dialer{}).DialContext(ctx, "tcp", ircAddr)
		}
	}
	conn, err := dial(ctx)
	if err != nil {
		return false, err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()

	c.mu.Lock()
	c.conn = conn
	c.isMod = false
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
		return false, err
	}

	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 64*1024), 64*1024)
	for {
		if err := conn.SetReadDeadline(time.Now().Add(readTimeout)); err != nil {
			return loggedIn, err
		}
		if !sc.Scan() {
			break
		}
		line := parseLine(sc.Text())
		switch line.command {
		case "PING":
			c.mu.Lock()
			err := c.write("PONG :%s", line.trailing)
			c.mu.Unlock()
			if err != nil {
				return loggedIn, err
			}
		case "RECONNECT":
			return loggedIn, errors.New("server requested reconnect")
		case "NOTICE":
			if strings.Contains(line.trailing, "authentication failed") ||
				strings.Contains(line.trailing, "Improperly formatted auth") {
				return loggedIn, errAuth
			}
		case "001":
			loggedIn = true
			log.Printf("twitch: connected to #%s", c.Channel)
		case "USERSTATE":
			c.mu.Lock()
			c.isMod = line.isMod()
			c.mu.Unlock()
		case "PRIVMSG":
			if c.OnMessage != nil {
				c.OnMessage(line.message())
			}
		}
	}
	if err := sc.Err(); err != nil {
		return loggedIn, err
	}
	return loggedIn, errors.New("connection closed")
}

// write sends one IRC line; callers must hold c.mu.
func (c *Client) write(format string, args ...any) error {
	if err := c.conn.SetWriteDeadline(time.Now().Add(writeTimeout)); err != nil {
		return err
	}
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
			l.tags[k] = unescapeTag(v)
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

// unescapeTag decodes an IRCv3 tag value, where `\:` is ';', `\s` is a
// space and `\\` is a backslash.
func unescapeTag(v string) string {
	if !strings.Contains(v, `\`) {
		return v
	}
	var b strings.Builder
	for i := 0; i < len(v); i++ {
		if v[i] != '\\' {
			b.WriteByte(v[i])
			continue
		}
		i++
		if i == len(v) {
			break // a trailing lone backslash is dropped
		}
		switch v[i] {
		case ':':
			b.WriteByte(';')
		case 's':
			b.WriteByte(' ')
		case 'r':
			b.WriteByte('\r')
		case 'n':
			b.WriteByte('\n')
		default:
			b.WriteByte(v[i])
		}
	}
	return b.String()
}

// isMod reports whether the line's sender is a moderator or the broadcaster.
func (l ircLine) isMod() bool {
	return l.tags["mod"] == "1" || strings.Contains(l.tags["badges"], "broadcaster/")
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
	text := l.trailing
	// "/me hello" arrives as "\x01ACTION hello\x01"; unwrap it so commands
	// still work.
	if action, ok := strings.CutPrefix(text, "\x01ACTION "); ok {
		text = strings.TrimSuffix(action, "\x01")
	}
	return chat.Message{
		Platform: chat.Twitch,
		Channel:  channel,
		User:     user,
		IsMod:    l.isMod(),
		Text:     text,
	}
}
