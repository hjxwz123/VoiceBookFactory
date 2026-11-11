package core

import (
	"regexp"
	"strings"
	"testing"
)

// The catalog may only contain recordings that can be redistributed and used commercially without conditions.
func TestMusicCatalogIsSafeToRedistribute(t *testing.T) {
	cat := MusicCatalog()
	if len(cat.Tracks) < 200 {
		t.Fatalf("catalog too small: %d", len(cat.Tracks))
	}
	known := map[string]bool{}
	for _, c := range cat.Categories {
		known[c.Key] = true
	}
	ids := map[string]bool{}
	per := map[string]int{}
	for _, tr := range cat.Tracks {
		if tr.License != "CC0" && tr.License != "公有领域" {
			t.Errorf("%s: license %q not allowed", tr.Title, tr.License)
		}
		if !strings.HasPrefix(tr.URL, "https://") || tr.Page == "" {
			t.Errorf("%s: missing https url or source page", tr.Title)
		}
		if !known[tr.Category] || tr.CategoryName == "" || len(tr.Tags) == 0 {
			t.Errorf("%s: bad category %q", tr.Title, tr.Category)
		}
		if ids[tr.ID] {
			t.Errorf("duplicate id %s", tr.ID)
		}
		ids[tr.ID] = true
		per[tr.Category]++
	}
	for _, c := range cat.Categories {
		if per[c.Key] < 5 {
			t.Errorf("category %s has only %d tracks", c.Name, per[c.Key])
		}
	}
}

// Isolated orchestra stems ("Bassoon 01", "DB 02 L") are not music anyone wants under a story.
func TestMusicCatalogHasNoStems(t *testing.T) {
	stem := regexp.MustCompile(`(?i)^(bassoon|celli|cello|clarinet|contra ?bass?on|db|decca|violins?|violas?)\b[\w ]*\d|backing track`)
	for _, tr := range MusicCatalog().Tracks {
		if stem.MatchString(tr.Title) {
			t.Errorf("stem in catalog: %s", tr.Title)
		}
	}
	famous := 0
	for _, tr := range MusicCatalog().Tracks {
		if tr.Category == "famous" {
			famous++
			if !regexp.MustCompile(`\p{Han}`).MatchString(tr.Title) {
				t.Errorf("famous piece without a Chinese title: %s", tr.Title)
			}
		}
	}
	if famous < 20 {
		t.Errorf("only %d famous pieces", famous)
	}
}

// Same rules for the sound-effect library, plus usable clip hints.
func TestSfxCatalogIsSafeToRedistribute(t *testing.T) {
	cat := SfxCatalog()
	if len(cat.Sounds) < 250 {
		t.Fatalf("catalog too small: %d", len(cat.Sounds))
	}
	known := map[string]bool{}
	for _, c := range cat.Categories {
		known[c.Key] = true
	}
	ids, per := map[string]bool{}, map[string]int{}
	for _, s := range cat.Sounds {
		if s.License != "CC0" && s.License != "公有领域" {
			t.Errorf("%s: license %q not allowed", s.Name, s.License)
		}
		if !strings.HasPrefix(s.URL, "https://") || s.Page == "" {
			t.Errorf("%s: missing https url or source page", s.Name)
		}
		if strings.Contains(strings.ToLower(s.URL), ".7z") || strings.Contains(strings.ToLower(s.URL), ".rar") {
			t.Errorf("%s: archive the server cannot open", s.Name)
		}
		if (s.Member != "" || s.Steps != nil) && !strings.HasSuffix(strings.ToLower(strings.Split(s.URL, "?")[0]), ".zip") {
			t.Errorf("%s: zip member on a non-zip source", s.Name)
		}
		if !known[s.Category] || s.CategoryName == "" || len(s.Tags) == 0 || (s.Kind != "sfx" && s.Kind != "ambience") {
			t.Errorf("%s: bad category/kind %q %q", s.Name, s.Category, s.Kind)
		}
		if ids[s.ID] {
			t.Errorf("duplicate id %s", s.ID)
		}
		ids[s.ID] = true
		per[s.Category]++
	}
	for _, c := range cat.Categories {
		if per[c.Key] < 8 {
			t.Errorf("category %s has only %d sounds", c.Name, per[c.Key])
		}
	}
}
