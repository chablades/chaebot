// Package commands implements dynamic custom commands that mods manage
// live from chat with $addcommand, $editcommand and $delcommand.
package commands

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

var (
	ErrExists   = errors.New("command already exists")
	ErrNotFound = errors.New("command not found")
)

// Store is the shared custom command list, persisted as a JSON object
// mapping trigger (e.g. "$discord") to response. It is safe for concurrent
// use so both platform listeners can share one instance.
type Store struct {
	mu   sync.RWMutex
	path string
	cmds map[string]string
}

// Open loads the store from path, starting empty if the file doesn't exist.
func Open(path string) (*Store, error) {
	s := &Store{path: path, cmds: map[string]string{}}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &s.cmds); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return s, nil
}

func (s *Store) Get(trigger string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	resp, ok := s.cmds[trigger]
	return resp, ok
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
	return s.update(func() error {
		if _, ok := s.cmds[trigger]; ok {
			return ErrExists
		}
		s.cmds[trigger] = response
		return nil
	}, trigger)
}

func (s *Store) Edit(trigger, response string) error {
	return s.update(func() error {
		if _, ok := s.cmds[trigger]; !ok {
			return ErrNotFound
		}
		s.cmds[trigger] = response
		return nil
	}, trigger)
}

func (s *Store) Delete(trigger string) error {
	return s.update(func() error {
		if _, ok := s.cmds[trigger]; !ok {
			return ErrNotFound
		}
		delete(s.cmds, trigger)
		return nil
	}, trigger)
}

// update applies change under the write lock and persists the result,
// restoring trigger's previous state if the save fails.
func (s *Store) update(change func() error, trigger string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	prev, existed := s.cmds[trigger]
	if err := change(); err != nil {
		return err
	}
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

// save writes atomically via a temp file so a crash can't corrupt the list.
func (s *Store) save() error {
	data, err := json.MarshalIndent(s.cmds, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
