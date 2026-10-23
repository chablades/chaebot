// Package commands implements dynamic custom commands that mods manage
// live from chat with $addcommand, $editcommand, $delcommand and $cooldown.
package commands

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"

	"chaebot/internal/jsonfile"
)

var (
	ErrExists   = errors.New("command already exists")
	ErrNotFound = errors.New("command not found")
)

// DefaultCooldown is the cooldown, in seconds, a new command starts with.
const DefaultCooldown = 5

// Command is one custom command.
type Command struct {
	Response string `json:"response"`
	Cooldown int    `json:"cooldown"` // seconds between uses by viewers
	Count    int    `json:"count"`    // times used, shown by {count}
}

// Store is the shared custom command list, persisted as a JSON object
// mapping trigger (e.g. "$discord") to Command. It is safe for concurrent
// use so every platform listener can share one instance.
type Store struct {
	mu   sync.RWMutex
	path string
	cmds map[string]Command
}

// Open loads the store from path, starting empty if the file doesn't exist.
// Files from before commands had settings, mapping trigger to response
// text, are upgraded on the next save.
func Open(path string) (*Store, error) {
	s := &Store{path: path, cmds: map[string]Command{}}
	var raw map[string]json.RawMessage
	if err := jsonfile.Load(path, &raw); err != nil {
		return nil, err
	}
	for trigger, v := range raw {
		var cmd Command
		var response string
		if json.Unmarshal(v, &response) == nil {
			cmd = Command{Response: response, Cooldown: DefaultCooldown}
		} else if err := json.Unmarshal(v, &cmd); err != nil {
			return nil, fmt.Errorf("parse %s: command %s: %w", path, trigger, err)
		}
		s.cmds[trigger] = cmd
	}
	return s, nil
}

func (s *Store) Get(trigger string) (Command, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cmd, ok := s.cmds[trigger]
	return cmd, ok
}

// Triggers returns all command triggers in sorted order.
func (s *Store) Triggers() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.cmds))
	for t := range s.cmds {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

func (s *Store) Add(trigger, response string) error {
	return s.update(trigger, func(cmd *Command, exists bool) error {
		if exists {
			return ErrExists
		}
		*cmd = Command{Response: response, Cooldown: DefaultCooldown}
		return nil
	})
}

// Edit changes a command's response, keeping its cooldown and count.
func (s *Store) Edit(trigger, response string) error {
	return s.update(trigger, func(cmd *Command, exists bool) error {
		if !exists {
			return ErrNotFound
		}
		cmd.Response = response
		return nil
	})
}

func (s *Store) SetCooldown(trigger string, seconds int) error {
	return s.update(trigger, func(cmd *Command, exists bool) error {
		if !exists {
			return ErrNotFound
		}
		cmd.Cooldown = seconds
		return nil
	})
}

// Increment adds one to a command's use count and returns the new count.
func (s *Store) Increment(trigger string) (int, error) {
	var n int
	err := s.update(trigger, func(cmd *Command, exists bool) error {
		if !exists {
			return ErrNotFound
		}
		cmd.Count++
		n = cmd.Count
		return nil
	})
	return n, err
}

func (s *Store) Delete(trigger string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	prev, ok := s.cmds[trigger]
	if !ok {
		return ErrNotFound
	}
	delete(s.cmds, trigger)
	if err := s.save(); err != nil {
		s.cmds[trigger] = prev
		return err
	}
	return nil
}

// update applies change to trigger's command under the write lock and
// persists the result, restoring the previous state if the save fails.
func (s *Store) update(trigger string, change func(cmd *Command, exists bool) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	prev, existed := s.cmds[trigger]
	cmd := prev
	if err := change(&cmd, existed); err != nil {
		return err
	}
	s.cmds[trigger] = cmd
	if err := s.save(); err != nil {
		if existed {
			s.cmds[trigger] = prev
		} else {
			delete(s.cmds, trigger)
		}
		return err
	}
	return nil
}

func (s *Store) save() error {
	return jsonfile.Save(s.path, s.cmds, 0o644)
}
