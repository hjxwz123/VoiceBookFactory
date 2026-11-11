package db_test

import (
	"strings"
	"testing"
	"time"

	"voicebook/internal/db"
	"voicebook/internal/db/dbtest"
	"voicebook/internal/model"
)

// The helpers must mean the same thing on SQLite and PostgreSQL (CI runs this on both).
func TestDialectHelpers(t *testing.T) {
	g := dbtest.Open(t)
	when := time.Date(2026, 11, 11, 23, 30, 0, 0, time.UTC)
	assets := []model.Asset{
		{Scope: model.ScopeSystem, Kind: model.AssetBGM, Name: "Rainy Night 雨夜", Tags: model.Strings("悬疑", "Piano"), Meta: model.Map{V: map[string]any{"category": "suspense", "bookId": "b1"}}},
		{Scope: model.ScopeSystem, Kind: model.AssetBGM, Name: "晨光", Tags: model.Strings("舒缓"), Meta: model.Map{V: map[string]any{"category": "calm"}}},
	}
	for i := range assets {
		assets[i].CreatedAt = when
		if err := g.Create(&assets[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	count := func(where string, args ...any) int64 {
		var n int64
		if err := g.Model(&model.Asset{}).Where(where, args...).Count(&n).Error; err != nil {
			t.Fatalf("%s: %v", where, err)
		}
		return n
	}
	if n := count(db.Like(g, "name"), "%rainy%"); n != 1 {
		t.Fatalf("Like must ignore letter case, got %d", n)
	}
	if n := count(db.Like(g, "tags"), "%\"piano\"%"); n != 1 {
		t.Fatalf("Like on a JSON column, got %d", n)
	}
	if n := count(db.Like(g, "tags"), "%悬疑%"); n != 1 {
		t.Fatalf("Like on a JSON column with Chinese text, got %d", n)
	}
	if n := count(db.NotLike(g, "tags"), "%悬疑%"); n != 1 {
		t.Fatalf("NotLike, got %d", n)
	}
	if n := count(db.JSONText(g, "meta", "bookId")+" = ?", "b1"); n != 1 {
		t.Fatalf("JSONText, got %d", n)
	}
	var days []string
	g.Model(&model.Asset{}).Select(db.Day(g, "created_at")+" AS day").Pluck("day", &days)
	if len(days) != 2 || days[0] != "2026-11-11" {
		t.Fatalf("Day = %v", days)
	}
}

// SQLite never enforced varchar lengths; on PostgreSQL long values must still fit.
func TestLongStringsFit(t *testing.T) {
	g := dbtest.Open(t)
	u := model.User{Nickname: strings.Repeat("长", 300), Bio: strings.Repeat("b", 2000)}
	if err := g.Create(&u).Error; err != nil {
		t.Fatalf("a nickname longer than its size tag must be stored: %v", err)
	}
	var back model.User
	if err := g.First(&back, "id = ?", u.ID).Error; err != nil || len([]rune(back.Nickname)) != 300 {
		t.Fatalf("read back %d runes, err %v", len([]rune(back.Nickname)), err)
	}
}
