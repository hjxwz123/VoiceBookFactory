package core

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"voicebook/internal/events"
)

func eventBook(bookID, scope string) events.Event {
	return events.Event{Type: "book", BookID: bookID, Data: map[string]any{"scope": scope}}
}

func eventChapter(bookID, chapterID, scope string) events.Event {
	return events.Event{Type: "chapter", BookID: bookID, ChapterID: chapterID, Data: map[string]any{"scope": scope}}
}

func eventSegments(bookID, chapterID string, segs any) events.Event {
	return events.Event{Type: "segments", BookID: bookID, ChapterID: chapterID, Data: segs}
}

func hashParts(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0x1f})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func f64(p *float64, def float64) float64 {
	if p == nil {
		return def
	}
	return *p
}

func fmtF(p *float64) string {
	if p == nil {
		return "-"
	}
	return fmt.Sprintf("%.3f", *p)
}

func runeSlice(s string, start, end int) string {
	r := []rune(s)
	if start < 0 {
		start = 0
	}
	if end > len(r) {
		end = len(r)
	}
	if start >= end {
		return ""
	}
	return string(r[start:end])
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// reporter receives progress from long operations (jobs.Context implements it).
type reporter interface {
	Progress(p float64, text string)
	SetResult(kv map[string]any)
}

// subReporter maps a sub-task's progress into a slice of a parent's progress bar.
type subReporter struct {
	parent reporter
	base   float64
	span   float64
	prefix string
}

func (s *subReporter) Progress(p float64, text string) {
	if s.parent == nil {
		return
	}
	if s.prefix != "" && text != "" {
		text = s.prefix + "：" + text
	}
	s.parent.Progress(s.base+p*s.span, text)
}

func (s *subReporter) SetResult(map[string]any) {}

type nopReporter struct{}

func (nopReporter) Progress(float64, string) {}
func (nopReporter) SetResult(map[string]any) {}
