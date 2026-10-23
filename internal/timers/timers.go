// Package timers posts recurring messages that mods manage from chat with
// $addtimer, $deltimer and $timers.
package timers

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"chaebot/internal/chat"
	"chaebot/internal/jsonfile"
)

const (
	addCmd  = chat.Prefix + "addtimer"
	delCmd  = chat.Prefix + "deltimer"
	listCmd = chat.Prefix + "timers"
)

// Builtins are the chat commands this package handles.
var Builtins = []string{addCmd, delCmd, listCmd}

const (
	minInterval = 5    // minutes
	maxInterval = 1440 // minutes
	// MinLines is how many chat messages a platform needs since a timer
	// last posted there before it posts again, so timers don't fill a
	// quiet chat.
	MinLines = 5
	tick     = 30 * time.Second
)

// Timer is one recurring message.
type Timer struct {
	Interval int    `json:"interval"` // minutes
	Message  string `json:"message"`
}

// key identifies a timer's state on one platform.
type key struct {
	name     string
	platform chat.Platform
}

// Scheduler holds the timers, tracks chat activity, and posts due timers.
// Put it first in the handler list: it counts every message to measure
// activity, and consumes only its own commands.
type Scheduler struct {
	path string
	now  func() time.Time // time.Now; set by tests

	mu       sync.Mutex
	timers   map[string]Timer
	lines    map[chat.Platform]int // messages seen per platform
	lastPost map[key]time.Time
	linesAt  map[key]int // lines[platform] when the timer last posted
}

// Open loads timers from path, starting empty if the file doesn't exist.
func Open(path string) (*Scheduler, error) {
	s := &Scheduler{
		path:     path,
		now:      time.Now,
		timers:   map[string]Timer{},
		lines:    map[chat.Platform]int{},
		lastPost: map[key]time.Time{},
		linesAt:  map[key]int{},
	}
	if err := jsonfile.Load(path, &s.timers); err != nil {
		return nil, err
	}
	return s, nil
}

// Len returns the number of timers.
func (s *Scheduler) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.timers)
}

// Run posts due timers until ctx is cancelled. sayers maps each connected
// platform to its send function.
func (s *Scheduler) Run(ctx context.Context, sayers map[chat.Platform]func(string)) {
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.Tick(sayers)
		}
	}
}

// Tick posts at most one due timer per platform: the one waiting longest.
// A timer is due on a platform once its interval has passed and that chat
// has had MinLines messages since the timer last posted there.
func (s *Scheduler) Tick(sayers map[chat.Platform]func(string)) {
	s.mu.Lock()
	now := s.now()
	type post struct {
		say  func(string)
		text string
	}
	var posts []post
	for p, say := range sayers {
		var best string
		var bestAt time.Time
		for name, t := range s.timers {
			k := key{name, p}
			last, ok := s.lastPost[k]
			if !ok {
				// Wait a full interval after startup or creation.
				s.lastPost[k], s.linesAt[k] = now, s.lines[p]
				continue
			}
			if now.Sub(last) < time.Duration(t.Interval)*time.Minute || s.lines[p]-s.linesAt[k] < MinLines {
				continue
			}
			if best == "" || last.Before(bestAt) || (last.Equal(bestAt) && name < best) {
				best, bestAt = name, last
			}
		}
		if best != "" {
			k := key{best, p}
			s.lastPost[k], s.linesAt[k] = now, s.lines[p]
			posts = append(posts, post{say, s.timers[best].Message})
		}
	}
	s.mu.Unlock()
	for _, p := range posts {
		p.say(p.text)
	}
}

func (s *Scheduler) Handle(msg chat.Message, reply func(string)) bool {
	s.mu.Lock()
	s.lines[msg.Platform]++
	s.mu.Unlock()

	name, rest := chat.CutWord(msg.Text)
	name = strings.ToLower(name)
	switch name {
	case addCmd, delCmd, listCmd:
		if msg.IsMod {
			reply(fmt.Sprintf("@%s %s", msg.User, s.manage(name, rest)))
		}
		return true
	}
	return false
}

func (s *Scheduler) manage(cmd, args string) string {
	name, rest := chat.CutWord(args)
	name = strings.ToLower(name)
	switch cmd {
	case listCmd:
		return s.list()
	case delCmd:
		if name == "" {
			return "Usage: " + delCmd + " name"
		}
		return s.update(name, func() string {
			if _, ok := s.timers[name]; !ok {
				return fmt.Sprintf("Timer %s doesn't exist.", name)
			}
			delete(s.timers, name)
			return ""
		}, fmt.Sprintf("Timer %s deleted.", name))
	}

	usage := fmt.Sprintf("Usage: %s name minutes message (every %d to %d minutes)", addCmd, minInterval, maxInterval)
	minutesText, message := chat.CutWord(rest)
	minutes, err := strconv.Atoi(minutesText)
	if name == "" || message == "" || err != nil || minutes < minInterval || minutes > maxInterval {
		return usage
	}
	if problem := chat.CheckText(message); problem != "" {
		return problem
	}
	return s.update(name, func() string {
		if _, ok := s.timers[name]; ok {
			return fmt.Sprintf("Timer %s already exists. Use %s %s first to replace it.", name, delCmd, name)
		}
		s.timers[name] = Timer{Interval: minutes, Message: message}
		return ""
	}, fmt.Sprintf("Timer %s added, posting every %d minutes while chat is active.", name, minutes))
}

// update applies change under the lock and saves, restoring name's previous
// state if the save fails. change returns a problem to report, or "" to
// save and report ok.
func (s *Scheduler) update(name string, change func() string, ok string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	prev, existed := s.timers[name]
	if problem := change(); problem != "" {
		return problem
	}
	if err := jsonfile.Save(s.path, s.timers, 0o644); err != nil {
		if existed {
			s.timers[name] = prev
		} else {
			delete(s.timers, name)
		}
		log.Printf("timers: %v", err)
		return "Couldn't save the timer list, check the bot logs."
	}
	// Restart the timer's schedule on every platform.
	for k := range s.lastPost {
		if k.name == name {
			delete(s.lastPost, k)
			delete(s.linesAt, k)
		}
	}
	return ok
}

func (s *Scheduler) list() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.timers) == 0 {
		return "No timers yet."
	}
	names := make([]string, 0, len(s.timers))
	for name := range s.timers {
		names = append(names, name)
	}
	sort.Strings(names)
	for i, name := range names {
		names[i] = fmt.Sprintf("%s (%dm)", name, s.timers[name].Interval)
	}
	return chat.Truncate("Timers: "+strings.Join(names, ", "), chat.MaxLen)
}
