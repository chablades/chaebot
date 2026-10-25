package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/runtime"

	"chaebot/internal/chat"
	"chaebot/internal/commands"
	"chaebot/internal/config"
	"chaebot/internal/twitch"
)

// Bot states shown in the UI's status light.
const (
	stateStopped      = "stopped"
	stateConnecting   = "connecting"
	stateConnected    = "connected"
	stateReconnecting = "reconnecting"
	stateError        = "error"
)

// maxLogLines is how much of the log the UI can catch up on when it loads.
const maxLogLines = 300

// Status is the bot's connection state.
type Status struct {
	State  string `json:"state"`
	Detail string `json:"detail"`
}

// ChatLine is one line in the UI's chat feed.
type ChatLine struct {
	Time    string `json:"time"`
	User    string `json:"user"`
	Text    string `json:"text"`
	IsMod   bool   `json:"isMod"`
	FromBot bool   `json:"fromBot"`
	Error   string `json:"error,omitempty"`
}

// State is everything the UI needs when it first loads.
type State struct {
	Status   Status             `json:"status"`
	Settings Settings           `json:"settings"`
	Commands []commands.Command `json:"commands"`
	Logs     []string           `json:"logs"`
	DataDir  string             `json:"dataDir"`
}

// App is the desktop backend. Its exported methods are callable from the
// frontend as window.go.main.App.<Method>, and it pushes "status", "chat",
// "commands" and "log" events to it.
type App struct {
	ctx     context.Context
	dataDir string
	store   *commands.Store
	handler *commands.Handler
	initErr error // why the bot can't run at all, if anything
	logFile *os.File

	mu       sync.Mutex
	settings Settings
	client   *twitch.Client
	cancel   context.CancelFunc
	done     chan struct{}
	status   Status

	logMu sync.Mutex // separate from mu so logging never deadlocks
	logs  []string
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.status = Status{State: stateStopped}
	log.SetFlags(log.Ltime)
	log.SetOutput(logWriter{a})

	dir, err := config.DataDir()
	if err != nil {
		a.fail(err)
		return
	}
	a.dataDir = dir
	if err := os.MkdirAll(dir, 0o755); err != nil {
		a.fail(err)
		return
	}
	if f, err := os.Create(filepath.Join(dir, "chaebot.log")); err == nil {
		a.logFile = f
		log.SetOutput(io.MultiWriter(logWriter{a}, f))
	}

	settings, err := loadSettings(dir)
	if err != nil {
		a.fail(fmt.Errorf("read settings: %w", err))
		return
	}
	a.settings = settings

	store, err := commands.Open(filepath.Join(dir, "commands.json"))
	if err != nil {
		a.fail(err)
		return
	}
	store.OnChange = func() { runtime.EventsEmit(ctx, "commands", store.List()) }
	a.store, a.handler = store, commands.NewHandler(store)
	log.Printf("loaded %d custom commands from %s", len(store.List()), dir)

	if settings.AutoStart && settings.complete() {
		if err := a.Start(); err != nil {
			log.Printf("auto-start: %v", err)
		}
	}
}

func (a *App) shutdown(context.Context) {
	a.Stop()
	if a.logFile != nil {
		a.logFile.Close()
	}
}

// focus brings the window forward when Chaebot is launched a second time.
func (a *App) focus(options.SecondInstanceData) {
	runtime.WindowUnminimise(a.ctx)
	runtime.WindowShow(a.ctx)
}

func (a *App) fail(err error) {
	a.initErr = err
	a.setStatus(stateError, err.Error())
	log.Printf("startup: %v", err)
}

// GetState returns the current status, settings, commands and recent log.
func (a *App) GetState() State {
	a.mu.Lock()
	st := State{Status: a.status, Settings: a.settings, DataDir: a.dataDir}
	a.mu.Unlock()
	st.Commands = a.ListCommands()
	a.logMu.Lock()
	st.Logs = append([]string(nil), a.logs...)
	a.logMu.Unlock()
	return st
}

// Start connects the bot to chat. It does nothing if the bot is running.
func (a *App) Start() error {
	if a.initErr != nil {
		return a.initErr
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cancel != nil {
		return nil
	}
	s := a.settings
	if !s.complete() {
		return errors.New("fill in the bot account, channel and token in Settings first")
	}

	ctx, cancel := context.WithCancel(a.ctx)
	done := make(chan struct{})
	client := &twitch.Client{Username: s.Username, Token: s.Token, Channel: s.Channel}
	handlers := []chat.Handler{a.handler}
	reply := func(text string) { a.say(client, text) }
	client.OnMessage = func(msg chat.Message) {
		a.emitChat(ChatLine{User: msg.User, Text: msg.Text, IsMod: msg.IsMod})
		chat.Dispatch(handlers, msg, reply)
	}
	client.OnConnect = func() { a.setStatus(stateConnected, "#"+s.Channel) }
	client.OnDisconnect = func(err error, retryIn time.Duration) {
		a.setStatus(stateReconnecting, fmt.Sprintf("retrying in %s", retryIn))
	}
	a.client, a.cancel, a.done = client, cancel, done
	a.status = Status{State: stateConnecting, Detail: "#" + s.Channel}
	runtime.EventsEmit(a.ctx, "status", a.status)

	go func() {
		defer close(done)
		err := client.Run(ctx)
		a.mu.Lock()
		if a.done == done {
			a.client, a.cancel, a.done = nil, nil, nil
		}
		a.mu.Unlock()
		if err != nil {
			log.Print(err)
			a.setStatus(stateError, err.Error())
		} else {
			a.setStatus(stateStopped, "")
		}
	}()
	return nil
}

// Stop disconnects the bot and waits for it to finish.
func (a *App) Stop() {
	a.mu.Lock()
	cancel, done := a.cancel, a.done
	a.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	<-done
}

// Send posts text to chat as the bot. The result, including a rate-limit
// drop, shows up in the chat feed.
func (a *App) Send(text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	a.mu.Lock()
	client := a.client
	a.mu.Unlock()
	if client == nil {
		return errors.New("start the bot first")
	}
	a.say(client, text)
	return nil
}

func (a *App) say(client *twitch.Client, text string) {
	line := ChatLine{User: client.Username, Text: text, FromBot: true}
	if err := client.Say(text); err != nil {
		line.Error = err.Error()
		log.Printf("twitch: %v", err)
	}
	a.emitChat(line)
}

// ListCommands returns all custom commands sorted by trigger.
func (a *App) ListCommands() []commands.Command {
	if a.store == nil {
		return nil
	}
	return a.store.List()
}

// AddCommand creates a custom command. The trigger may omit the prefix.
func (a *App) AddCommand(trigger, response string) error {
	trigger, response, err := a.checkCommand(trigger, response)
	if err != nil {
		return err
	}
	return describe(a.store.Add(trigger, response), trigger)
}

// EditCommand replaces an existing command's response.
func (a *App) EditCommand(trigger, response string) error {
	trigger, response, err := a.checkCommand(trigger, response)
	if err != nil {
		return err
	}
	return describe(a.store.Edit(trigger, response), trigger)
}

// DeleteCommand removes a custom command.
func (a *App) DeleteCommand(trigger string) error {
	if a.store == nil {
		return a.initErr
	}
	trigger = normalizeTrigger(trigger)
	return describe(a.store.Delete(trigger), trigger)
}

// checkCommand applies the same rules as the chat management commands.
func (a *App) checkCommand(trigger, response string) (string, string, error) {
	if a.store == nil {
		return "", "", a.initErr
	}
	trigger = normalizeTrigger(trigger)
	// Chat messages are one line, so a response can't hold line breaks.
	response = strings.TrimSpace(strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ").Replace(response))
	if response == "" {
		return "", "", errors.New("type a response")
	}
	if problem := commands.Validate(trigger, response); problem != "" {
		return "", "", errors.New(problem)
	}
	return trigger, response, nil
}

func normalizeTrigger(t string) string {
	t = strings.ToLower(strings.TrimSpace(t))
	if !strings.HasPrefix(t, chat.Prefix) {
		t = chat.Prefix + t
	}
	return t
}

func describe(err error, trigger string) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, commands.ErrExists):
		return fmt.Errorf("%s already exists", trigger)
	case errors.Is(err, commands.ErrNotFound):
		return fmt.Errorf("%s doesn't exist", trigger)
	default:
		log.Printf("commands: %v", err)
		return fmt.Errorf("couldn't save the command list: %v", err)
	}
}

// GetSettings returns the saved Twitch login details.
func (a *App) GetSettings() Settings {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.settings
}

// SaveSettings stores new login details. A running bot keeps its current
// connection until it is restarted.
func (a *App) SaveSettings(s Settings) error {
	if a.dataDir == "" {
		return a.initErr
	}
	s = s.normalized()
	if err := saveSettings(a.dataDir, s); err != nil {
		return fmt.Errorf("couldn't save settings: %v", err)
	}
	a.mu.Lock()
	a.settings = s
	a.mu.Unlock()
	return nil
}

// OpenDataFolder shows the folder holding commands.json, settings and the
// log in Explorer.
func (a *App) OpenDataFolder() error {
	if a.dataDir == "" {
		return a.initErr
	}
	return exec.Command("explorer", a.dataDir).Start()
}

func (a *App) setStatus(state, detail string) {
	a.mu.Lock()
	a.status = Status{State: state, Detail: detail}
	st := a.status
	a.mu.Unlock()
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, "status", st)
	}
}

func (a *App) emitChat(line ChatLine) {
	line.Time = time.Now().Format("15:04")
	runtime.EventsEmit(a.ctx, "chat", line)
}

// logWriter mirrors the standard logger into the UI's log panel.
type logWriter struct{ a *App }

func (w logWriter) Write(p []byte) (int, error) {
	line := strings.TrimRight(string(p), "\n")
	w.a.logMu.Lock()
	w.a.logs = append(w.a.logs, line)
	if len(w.a.logs) > maxLogLines {
		w.a.logs = w.a.logs[len(w.a.logs)-maxLogLines:]
	}
	w.a.logMu.Unlock()
	if w.a.ctx != nil {
		runtime.EventsEmit(w.a.ctx, "log", line)
	}
	return len(p), nil
}
