// Package events fans out realtime events to connected clients (served over SSE).
package events

import (
	"sync"
	"time"
)

type Event struct {
	Type      string    `json:"type"`
	StudioID  string    `json:"studioId,omitempty"`
	BookID    string    `json:"bookId,omitempty"`
	ChapterID string    `json:"chapterId,omitempty"`
	Data      any       `json:"data,omitempty"`
	At        time.Time `json:"at"`
}

type Subscriber struct {
	UserID  string
	studios map[string]bool
	C       chan Event
	mu      sync.RWMutex
}

func (s *Subscriber) SetStudios(ids []string) {
	m := make(map[string]bool, len(ids))
	for _, id := range ids {
		m[id] = true
	}
	s.mu.Lock()
	s.studios = m
	s.mu.Unlock()
}

func (s *Subscriber) inStudio(id string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.studios[id]
}

type Hub struct {
	mu   sync.RWMutex
	subs map[*Subscriber]struct{}
}

func NewHub() *Hub { return &Hub{subs: map[*Subscriber]struct{}{}} }

func (h *Hub) Subscribe(userID string, studios []string) (*Subscriber, func()) {
	s := &Subscriber{UserID: userID, C: make(chan Event, 256)}
	s.SetStudios(studios)
	h.mu.Lock()
	h.subs[s] = struct{}{}
	h.mu.Unlock()
	return s, func() {
		h.mu.Lock()
		delete(h.subs, s)
		h.mu.Unlock()
	}
}

func (h *Hub) send(s *Subscriber, ev Event) {
	select {
	case s.C <- ev:
	default: // slow consumer: drop rather than block producers
	}
}

// Studio publishes to every connected member of a studio.
func (h *Hub) Studio(studioID string, ev Event) {
	if ev.At.IsZero() {
		ev.At = time.Now()
	}
	ev.StudioID = studioID
	h.mu.RLock()
	defer h.mu.RUnlock()
	for s := range h.subs {
		if s.inStudio(studioID) {
			h.send(s, ev)
		}
	}
}

// User publishes to all connections of one user.
func (h *Hub) User(userID string, ev Event) {
	if ev.At.IsZero() {
		ev.At = time.Now()
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for s := range h.subs {
		if s.UserID == userID {
			h.send(s, ev)
		}
	}
}

// Online returns the number of connected subscribers.
func (h *Hub) Online() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.subs)
}
