package core

import (
	"sort"
	"sync"
	"time"

	"voicebook/internal/events"
	"voicebook/internal/model"
)

// Presence tracks who is working in a chapter and which line they are on (realtime collaboration).
type presenceEntry struct {
	UserID    string    `json:"userId"`
	Nickname  string    `json:"nickname"`
	AvatarURL string    `json:"avatarUrl"`
	SegmentID string    `json:"segmentId"`
	Color     string    `json:"color"`
	At        time.Time `json:"at"`
}

var (
	presenceMu sync.Mutex
	presence   = map[string]map[string]*presenceEntry{}
)

var presenceColors = []string{"#6366f1", "#ec4899", "#0ea5e9", "#f59e0b", "#10b981", "#8b5cf6", "#ef4444", "#14b8a6"}

func (a *App) Presence(chapterID string) []presenceEntry {
	presenceMu.Lock()
	defer presenceMu.Unlock()
	var out []presenceEntry
	for id, e := range presence[chapterID] {
		if time.Since(e.At) > 40*time.Second {
			delete(presence[chapterID], id)
			continue
		}
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Nickname < out[j].Nickname })
	if out == nil {
		out = []presenceEntry{}
	}
	return out
}

// Touch records a heartbeat (segmentID = currently focused line) and broadcasts the roster.
func (a *App) Touch(x *Access, ch *model.Chapter, segmentID string, leave bool) []presenceEntry {
	presenceMu.Lock()
	m := presence[ch.ID]
	if m == nil {
		m = map[string]*presenceEntry{}
		presence[ch.ID] = m
	}
	if leave {
		delete(m, x.User.ID)
	} else {
		e := m[x.User.ID]
		if e == nil {
			e = &presenceEntry{UserID: x.User.ID, Nickname: x.User.Nickname, AvatarURL: x.User.AvatarURL, Color: presenceColors[len(m)%len(presenceColors)]}
			m[x.User.ID] = e
		}
		e.SegmentID, e.At = segmentID, time.Now()
	}
	presenceMu.Unlock()
	list := a.Presence(ch.ID)
	a.emit(x.StudioID, events.Event{Type: "presence", BookID: ch.BookID, ChapterID: ch.ID, Data: list})
	return list
}
