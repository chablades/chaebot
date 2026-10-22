package commands

import (
	"errors"
	"fmt"
	"log"
	"strings"
	"unicode"
	"unicode/utf8"

	"chaebot/internal/chat"
)

// maxResponseLen is Twitch's per-message character limit.
const maxResponseLen = 500

const (
	addCmd  = "!addcommand"
	editCmd = "!editcommand"
	delCmd  = "!delcommand"
)

// Handler answers custom command triggers and the mod-only management
// commands. Any viewer may use a custom command; there are no cooldowns.
type Handler struct {
	store *Store
}

func NewHandler(store *Store) *Handler {
	return &Handler{store: store}
}

func (h *Handler) Handle(msg chat.Message, reply func(string)) bool {
	name, rest := cutWord(msg.Text)
	name = strings.ToLower(name)
	switch name {
	case addCmd, editCmd, delCmd:
		if msg.IsMod {
			reply(fmt.Sprintf("@%s %s", msg.User, h.manage(name, rest)))
		}
		return true
	}
	if resp, ok := h.store.Get(name); ok {
		reply(resp)
		return true
	}
	return false
}

// manage runs a management command and returns the text to post in chat.
func (h *Handler) manage(name, args string) string {
	trigger, response := cutWord(args)
	trigger = strings.ToLower(trigger)

	if name == delCmd {
		if trigger == "" {
			return "Usage: " + delCmd + " !trigger"
		}
		if err := h.store.Delete(trigger); err != nil {
			return describe(err, trigger)
		}
		return fmt.Sprintf("Command %s deleted.", trigger)
	}

	if trigger == "" || response == "" {
		return fmt.Sprintf("Usage: %s !trigger response text", name)
	}
	if problem := validate(trigger, response); problem != "" {
		return problem
	}
	if name == addCmd {
		if err := h.store.Add(trigger, response); err != nil {
			return describe(err, trigger)
		}
		return fmt.Sprintf("Command %s added.", trigger)
	}
	if err := h.store.Edit(trigger, response); err != nil {
		return describe(err, trigger)
	}
	return fmt.Sprintf("Command %s updated.", trigger)
}

// validate returns a chat-ready description of what's wrong, or "" if the
// command is acceptable.
func validate(trigger, response string) string {
	if len(trigger) < 2 || trigger[0] != '!' {
		return "Triggers must start with ! (e.g. !discord)."
	}
	switch trigger {
	case addCmd, editCmd, delCmd:
		return trigger + " is a built-in command and can't be overridden."
	}
	// Twitch treats lines starting with / or . as chat commands, so a
	// response like "/ban x" would run with the bot's permissions.
	if response[0] == '/' || response[0] == '.' {
		return "Responses can't start with / or ."
	}
	if utf8.RuneCountInString(response) > maxResponseLen {
		return fmt.Sprintf("Response is too long (max %d characters).", maxResponseLen)
	}
	return ""
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

// cutWord splits s into its first whitespace-delimited word and the
// trimmed remainder.
func cutWord(s string) (word, rest string) {
	s = strings.TrimSpace(s)
	if i := strings.IndexFunc(s, unicode.IsSpace); i >= 0 {
		return s[:i], strings.TrimSpace(s[i:])
	}
	return s, ""
}
