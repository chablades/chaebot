package twitch

import (
	"bufio"
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"chaebot/internal/chat"
)

// fakeTwitch serves one scripted connection per dial. Each script gets the
// PASS line the client logged in with and the server side of the pipe.
type fakeTwitch struct {
	t       *testing.T
	scripts chan func(pass string, conn net.Conn)
}

func newFakeTwitch(t *testing.T, scripts ...func(pass string, conn net.Conn)) *fakeTwitch {
	f := &fakeTwitch{t: t, scripts: make(chan func(string, net.Conn), len(scripts))}
	for _, s := range scripts {
		f.scripts <- s
	}
	return f
}

func (f *fakeTwitch) dial(ctx context.Context) (net.Conn, error) {
	var script func(string, net.Conn)
	select {
	case script = <-f.scripts:
	default:
		f.t.Error("unexpected extra dial")
		return nil, errors.New("no more scripts")
	}
	client, server := net.Pipe()
	go func() {
		defer server.Close()
		r := bufio.NewReader(server)
		var pass string
		for range 4 { // CAP, PASS, NICK, JOIN
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			if p, ok := strings.CutPrefix(line, "PASS "); ok {
				pass = strings.TrimSpace(p)
			}
		}
		// Keep reading so the client's writes (PONG, PRIVMSG) never block.
		go func() {
			for {
				if _, err := r.ReadString('\n'); err != nil {
					return
				}
			}
		}()
		script(pass, server)
	}()
	return client, nil
}

func send(conn net.Conn, lines ...string) {
	for _, l := range lines {
		conn.Write([]byte(l + "\r\n"))
	}
}

const authFailed = ":tmi.twitch.tv NOTICE * :Login authentication failed"

func TestRefreshOnAuthFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	fake := newFakeTwitch(t,
		func(pass string, conn net.Conn) {
			if pass != "oauth:old" {
				t.Errorf("first login used %q", pass)
			}
			send(conn, authFailed)
		},
		func(pass string, conn net.Conn) {
			if pass != "oauth:new" {
				t.Errorf("second login used %q", pass)
			}
			send(conn, ":tmi.twitch.tv 001 chae_bot :Welcome",
				":viewer!viewer@viewer.tmi.twitch.tv PRIVMSG #chablades :hi")
			<-ctx.Done()
		},
	)
	var got []chat.Message
	c := &Client{Username: "chae_bot", Token: "old", Channel: "chablades", dial: fake.dial}
	c.Refresh = func(context.Context) (string, error) { return "new", nil }
	c.OnMessage = func(m chat.Message) {
		got = append(got, m)
		cancel()
	}
	if err := c.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Text != "hi" {
		t.Errorf("messages = %+v", got)
	}
}

func TestAuthFailureAfterRefreshIsFatal(t *testing.T) {
	fake := newFakeTwitch(t,
		func(_ string, conn net.Conn) { send(conn, authFailed) },
		func(_ string, conn net.Conn) { send(conn, authFailed) },
	)
	var refreshes int
	c := &Client{Username: "chae_bot", Token: "old", Channel: "chablades", dial: fake.dial}
	c.Refresh = func(context.Context) (string, error) { refreshes++; return "new", nil }
	err := c.Run(context.Background())
	if !errors.Is(err, errAuth) {
		t.Fatalf("Run = %v, want auth error", err)
	}
	if refreshes != 1 {
		t.Errorf("refreshed %d times, want 1", refreshes)
	}
}

func TestRefreshDeniedIsFatal(t *testing.T) {
	fake := newFakeTwitch(t, func(_ string, conn net.Conn) { send(conn, authFailed) })
	c := &Client{Username: "chae_bot", Token: "old", Channel: "chablades", dial: fake.dial}
	c.Refresh = func(context.Context) (string, error) { return "", ErrRefreshDenied }
	if err := c.Run(context.Background()); !errors.Is(err, ErrRefreshDenied) {
		t.Fatalf("Run = %v, want ErrRefreshDenied", err)
	}
}

func TestSilentConnectionReconnects(t *testing.T) {
	old := readTimeout
	readTimeout = 50 * time.Millisecond
	defer func() { readTimeout = old }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	fake := newFakeTwitch(t,
		func(_ string, conn net.Conn) { <-ctx.Done() }, // never speaks
		func(_ string, conn net.Conn) {
			send(conn, ":viewer!viewer@viewer.tmi.twitch.tv PRIVMSG #chablades :back")
			<-ctx.Done()
		},
	)
	var got atomic.Bool
	c := &Client{Username: "chae_bot", Token: "t", Channel: "chablades", dial: fake.dial}
	c.OnMessage = func(chat.Message) { got.Store(true); cancel() }
	if err := c.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if !got.Load() {
		t.Error("never reconnected after the connection went silent")
	}
}

func TestRateLimitDependsOnModStatus(t *testing.T) {
	for _, tt := range []struct {
		mod  bool
		want int
	}{{false, rateLimit}, {true, modRateLimit}} {
		client, server := net.Pipe()
		lines := make(chan int)
		go func() {
			n := 0
			sc := bufio.NewScanner(server)
			for sc.Scan() {
				n++
			}
			lines <- n
		}()
		c := &Client{Channel: "chablades", conn: client, isMod: tt.mod}
		for range tt.want + 5 {
			c.Say("hi")
		}
		client.Close()
		if n := <-lines; n != tt.want {
			t.Errorf("mod=%v: sent %d messages, want %d", tt.mod, n, tt.want)
		}
	}
}

func TestUserstateSetsModStatus(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	fake := newFakeTwitch(t, func(_ string, conn net.Conn) {
		send(conn, "@badges=moderator/1;mod=1 :tmi.twitch.tv USERSTATE #chablades",
			":viewer!viewer@viewer.tmi.twitch.tv PRIVMSG #chablades :check")
		<-ctx.Done()
	})
	c := &Client{Username: "chae_bot", Token: "t", Channel: "chablades", dial: fake.dial}
	var isMod bool
	c.OnMessage = func(chat.Message) {
		c.mu.Lock()
		isMod = c.isMod
		c.mu.Unlock()
		cancel()
	}
	c.Run(ctx)
	if !isMod {
		t.Error("USERSTATE with mod=1 didn't mark the bot as a moderator")
	}
}

func TestRefresher(t *testing.T) {
	var form http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		form = *r
		if r.PostForm.Get("refresh_token") == "bad" {
			http.Error(w, `{"status":400,"message":"Invalid refresh token"}`, http.StatusBadRequest)
			return
		}
		w.Write([]byte(`{"access_token":"acc2","refresh_token":"ref2"}`))
	}))
	defer srv.Close()

	var saved [2]string
	r := &Refresher{ClientID: "id", ClientSecret: "secret", RefreshToken: "ref1", url: srv.URL,
		OnRefresh: func(a, rt string) { saved = [2]string{a, rt} }}
	tok, err := r.Refresh(context.Background())
	if err != nil || tok != "acc2" {
		t.Fatalf("Refresh = %q, %v", tok, err)
	}
	f := form.PostForm
	if f.Get("grant_type") != "refresh_token" || f.Get("refresh_token") != "ref1" ||
		f.Get("client_id") != "id" || f.Get("client_secret") != "secret" {
		t.Errorf("form = %v", f)
	}
	if r.RefreshToken != "ref2" || saved != [2]string{"acc2", "ref2"} {
		t.Errorf("rotated token not kept: field=%q saved=%q", r.RefreshToken, saved)
	}

	r.RefreshToken = "bad"
	if _, err := r.Refresh(context.Background()); !errors.Is(err, ErrRefreshDenied) {
		t.Errorf("bad refresh token: err = %v, want ErrRefreshDenied", err)
	}
}
