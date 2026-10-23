package commands

import (
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"chaebot/internal/chat"
)

const (
	addCmd      = chat.Prefix + "addcommand"
	editCmd     = chat.Prefix + "editcommand"
	delCmd      = chat.Prefix + "delcommand"
	cooldownCmd = chat.Prefix + "cooldown"
	listCmd     = chat.Prefix + "commands"
)

// maxCooldown is the longest cooldown mods may set, in seconds.
const maxCooldown = 3600

// Handler answers custom command triggers, $commands, and the mod-only
// management commands. Viewers are held to each command's cooldown;
// mods and the broadcaster are not.
type Handler struct {
	store    *Store
	reserved map[string]bool  // triggers custom commands can't use
	now      func() time.Time // time.Now; set by tests

	mu       sync.Mutex
	lastUsed map[string]time.Time // by platform + trigger
}

// NewHandler returns a handler for store. builtins are other features'
// command names, which custom commands may not take over.
func NewHandler(store *Store, builtins ...string) *Handler {
	h := &Handler{
		store:    store,
		reserved: map[string]bool{},
		now:      time.Now,
		lastUsed: map[string]time.Time{},
	}
	for _, name := range append([]string{addCmd, editCmd, delCmd, cooldownCmd, listCmd}, builtins...) {
		h.reserved[name] = true
	}
	return h
}

func (h *Handler) Handle(msg chat.Message, reply func(string)) bool {
	name, rest := chat.CutWord(msg.Text)
	name = strings.ToLower(name)
	switch name {
	case addCmd, editCmd, delCmd, cooldownCmd:
		if msg.IsMod {
			reply(fmt.Sprintf("@%s %s", msg.User, h.manage(name, rest)))
		}
		return true
	case listCmd:
		if h.ready(msg, listCmd, DefaultCooldown) {
			reply(h.list())
		}
		return true
	}
	cmd, ok := h.store.Get(name)
	if !ok {
		return false
	}
	if h.ready(msg, name, cmd.Cooldown) {
		reply(h.expand(name, cmd, msg, rest))
	}
	return true
}

// ready reports whether trigger may run for msg now, and if so starts its
// cooldown on msg's platform.
func (h *Handler) ready(msg chat.Message, trigger string, cooldown int) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	key := string(msg.Platform) + " " + trigger
	now := h.now()
	if !msg.IsMod && now.Sub(h.lastUsed[key]) < time.Duration(cooldown)*time.Second {
		return false
	}
	h.lastUsed[key] = now
	return true
}

// expand fills in a response's variables:
//
//	{user}    the person who used the command
//	{touser}  the first word after the command without a leading @, or {user}
//	{args}    everything after the command
//	{count}   how many times the command has been used, including this time
func (h *Handler) expand(trigger string, cmd Command, msg chat.Message, args string) string {
	count := ""
	if strings.Contains(cmd.Response, "{count}") {
		n, err := h.store.Increment(trigger)
		if err != nil {
			log.Printf("commands: count %s: %v", trigger, err)
		}
		count = strconv.Itoa(n)
	}
	touser, _ := chat.CutWord(args)
	touser = strings.TrimPrefix(touser, "@")
	if touser == "" {
		touser = msg.User
	}
	return strings.NewReplacer(
		"{user}", msg.User,
		"{touser}", touser,
		"{args}", args,
		"{count}", count,
	).Replace(cmd.Response)
}

func (h *Handler) list() string {
	triggers := h.store.Triggers()
	if len(triggers) == 0 {
		return "No custom commands yet."
	}
	out := "Commands: " + strings.Join(triggers, ", ")
	if len(out) > chat.MaxLen {
		out = chat.Truncate(out, chat.MaxLen-1) + "…"
	}
	return out
}

// manage runs a management command and returns the text to post in chat.
func (h *Handler) manage(name, args string) string {
	trigger, rest := chat.CutWord(args)
	trigger = strings.ToLower(trigger)

	switch name {
	case delCmd:
		if trigger == "" {
			return "Usage: " + delCmd + " " + chat.Prefix + "trigger"
		}
		if err := h.store.Delete(trigger); err != nil {
			return describe(err, trigger)
		}
		return fmt.Sprintf("Command %s deleted.", trigger)
	case cooldownCmd:
		seconds, err := strconv.Atoi(rest)
		if trigger == "" || err != nil || seconds < 0 || seconds > maxCooldown {
			return fmt.Sprintf("Usage: %s %strigger seconds (0 to %d)", cooldownCmd, chat.Prefix, maxCooldown)
		}
		if err := h.store.SetCooldown(trigger, seconds); err != nil {
			return describe(err, trigger)
		}
		return fmt.Sprintf("Command %s now has a %ds cooldown.", trigger, seconds)
	}

	if trigger == "" || rest == "" {
		return fmt.Sprintf("Usage: %s %strigger response text", name, chat.Prefix)
	}
	if problem := h.validate(trigger, rest); problem != "" {
		return problem
	}
	if name == addCmd {
		if err := h.store.Add(trigger, rest); err != nil {
			return describe(err, trigger)
		}
		return fmt.Sprintf("Command %s added.", trigger)
	}
	if err := h.store.Edit(trigger, rest); err != nil {
		return describe(err, trigger)
	}
	return fmt.Sprintf("Command %s updated.", trigger)
}

// validate returns a chat-ready description of what's wrong, or "" if the
// command is acceptable.
func (h *Handler) validate(trigger, response string) string {
	if len(trigger) <= len(chat.Prefix) || !strings.HasPrefix(trigger, chat.Prefix) {
		return fmt.Sprintf("Triggers must start with %s (e.g. %sdiscord).", chat.Prefix, chat.Prefix)
	}
	if h.reserved[trigger] {
		return trigger + " is a built-in command and can't be overridden."
	}
	return chat.CheckText(response)
}

func describe(err error, trigger string) string {
	switch {
	case errors.Is(err, ErrExists):
		return fmt.Sprintf("Command %s already exists. Use %s to change it.", trigger, editCmd)
	case errors.Is(err, ErrNotFound):
		return fmt.Sprintf("Command %s doesn't exist.", trigger)
	default:
		log.Printf("commands: %v", err)
		return "Couldn't save the command list, check the bot logs."
	}
}
