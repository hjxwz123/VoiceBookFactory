package core

import (
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"voicebook/internal/model"
)

// A long novel can have hundreds of characters. When the model annotates a chapter or looks for the
// characters in it, it is only told about the ones this chapter needs: those named in the text (by
// name or alias), the leads, and whoever spoke in the previous chapter. Speakers it reports as new
// are matched against the whole cast in code before anyone is added, so a smaller table never
// creates duplicates of characters the book already has.

const chapterCastLimit = 60

// chapterCast picks the characters worth telling the model about for one chapter, in book order.
func (a *App) chapterCast(ch *model.Chapter, chars []model.Character) []model.Character {
	prev := a.previousSpeakers(ch)
	type pick struct {
		id    string
		score int
	}
	var picks []pick
	for _, c := range chars {
		if c.Kind == model.CharNarrator {
			continue
		}
		score := 0
		if n := mentions(ch.SourceText, c); n > 0 {
			score = 1000 + min(n, 500)
		} else if prev[c.ID] {
			score = 500
		} else if c.Kind == model.CharLead {
			score = 300
		} else {
			continue
		}
		if c.Kind == model.CharLead {
			score += 200
		}
		picks = append(picks, pick{c.ID, score})
	}
	sort.SliceStable(picks, func(i, j int) bool { return picks[i].score > picks[j].score })
	if len(picks) > chapterCastLimit {
		picks = picks[:chapterCastLimit]
	}
	keep := map[string]bool{}
	for _, p := range picks {
		keep[p.id] = true
	}
	var out []model.Character
	for _, c := range chars {
		if keep[c.ID] {
			out = append(out, c)
		}
	}
	return out
}

// mentions counts how often a character is named in text. Aliases of one character ("他", "娘") say
// nothing about who is present, so only aliases of two or more characters count.
func mentions(text string, c model.Character) int {
	n := 0
	if c.Name != "" {
		n += strings.Count(text, c.Name)
	}
	for _, al := range c.Aliases.V {
		if utf8.RuneCountInString(al) >= 2 && al != c.Name {
			n += strings.Count(text, al)
		}
	}
	return n
}

// previousSpeakers lists who spoke in the nearest earlier chapter that has a script, so a chapter that
// continues a conversation ("他摇了摇头：……") still knows the people in it.
func (a *App) previousSpeakers(ch *model.Chapter) map[string]bool {
	var prev model.Chapter
	if a.DB.Select("id", "current_script_id").Where("book_id = ? AND \"index\" < ? AND current_script_id <> ''", ch.BookID, ch.Index).
		Order("\"index\" desc").Limit(1).Find(&prev).Error != nil || prev.ID == "" {
		return nil
	}
	var ids []string
	a.DB.Model(&model.Segment{}).Where("script_id = ? AND removed = ? AND type IN ? AND character_id <> ''", prev.CurrentScriptID, false, []string{model.SegDialogue, model.SegMonologue}).
		Distinct().Pluck("character_id", &ids)
	out := map[string]bool{}
	for _, id := range ids {
		out[id] = true
	}
	return out
}

// bookLocks serialises changes to one book's cast (adding discovered characters, casting them), so
// chapters processed side by side never add the same person twice.
var bookLocks sync.Map

func lockBook(bookID string) func() {
	v, _ := bookLocks.LoadOrStore(bookID, &sync.Mutex{})
	m := v.(*sync.Mutex)
	m.Lock()
	return m.Unlock
}

// findCharacter looks a name up among the book's characters, by name or alias, straight from the
// database (another chapter may have just added it).
func (a *App) findCharacter(bookID, name string) *model.Character {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	var chars []model.Character
	a.DB.Where("book_id = ?", bookID).Find(&chars)
	for i := range chars {
		if chars[i].Name == name || contains(chars[i].Aliases.V, name) {
			return &chars[i]
		}
	}
	return nil
}
