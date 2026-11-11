package core

import (
	"testing"

	"voicebook/internal/model"
)

func TestHelpDocsUpgradeOnlyUntouched(t *testing.T) {
	a, _ := testApp(t)
	a.DB.Where("kind = ?", "help").Delete(&model.Article{})
	// an old install: two first-release articles, one edited by an administrator
	for _, h := range helpV1 {
		h := h
		h.Published = true
		if h.Title == "智能审听" {
			h.Body = "运营自己改过的内容"
		}
		if h.Title == "三步做出第一本有声书" || h.Title == "智能审听" || h.Title == "什么是画本？" {
			a.DB.Create(&h)
		}
	}
	a.syncHelpDocs()
	a.syncHelpDocs() // idempotent

	var first model.Article
	if a.DB.Where("kind = ? AND title = ?", "help", "做出第一本有声书").First(&first).Error != nil {
		t.Fatal("untouched getting-started article should be replaced by the new one")
	}
	var n int64
	a.DB.Model(&model.Article{}).Where("kind = ? AND title = ?", "help", "三步做出第一本有声书").Count(&n)
	if n != 0 {
		t.Fatal("old title should be gone after an in-place rewrite")
	}
	var edited model.Article
	if a.DB.Where("kind = ? AND title = ?", "help", "智能审听").First(&edited).Error != nil || edited.Body != "运营自己改过的内容" {
		t.Fatal("an edited article must be left alone")
	}
	a.DB.Model(&model.Article{}).Where("kind = ? AND title = ?", "help", "审听与返工").Count(&n)
	if n != 0 {
		t.Fatal("no duplicate when the predecessor was edited")
	}
	var script model.Article
	a.DB.Where("kind = ? AND title = ?", "help", "什么是画本？").First(&script)
	if script.Body == helpV1[1].Body {
		t.Fatal("untouched article with the same title should get the new text")
	}
	for _, title := range []string{"读音表：人名和多音字不再读错", "版权须知"} {
		a.DB.Model(&model.Article{}).Where("kind = ? AND title = ?", "help", title).Count(&n)
		if n != 1 {
			t.Fatalf("new article %q should be added once, got %d", title, n)
		}
	}
}

func TestCreditGuide(t *testing.T) {
	a, _ := testApp(t)
	a.seedPricing()
	g := a.CreditGuide()
	per := g["per10kChars"].(map[string]int64)
	if !(per["economy"] > 0 && per["economy"] < per["standard"] && per["standard"] < per["premium"]) {
		t.Fatalf("per-10k costs should grow with the tier: %v", per)
	}
}

func TestLaunchCheck(t *testing.T) {
	a, _ := testApp(t)
	a.seedLegal()
	byKey := func() map[string]LaunchItem {
		m := map[string]LaunchItem{}
		for _, it := range a.LaunchCheck() {
			m[it.Key] = it
		}
		return m
	}
	m := byKey()
	if m["llm"].OK || m["operator"].OK || m["legal"].OK {
		t.Fatalf("fresh install should flag missing model, operator and default agreements: %+v", m)
	}
	// a test double on this machine is not a real connection
	a.DB.Create(&model.ProviderAccount{Kind: model.KindLLM, Provider: "openai_chat", Scope: "platform", Name: "本机模拟", BaseURL: "http://127.0.0.1:18999/v1", Enabled: true})
	m = byKey()
	if m["llm"].OK || m["local_accounts"].OK || m["local_accounts"].Key == "" {
		t.Fatalf("local account must not count and must be flagged: %+v", m["local_accounts"])
	}
	a.DB.Create(&model.ProviderAccount{Kind: model.KindLLM, Provider: "openai_chat", Scope: "platform", Name: "真实", BaseURL: "https://api.example.com/v1", Enabled: true})
	a.SetSetting("site", map[string]any{"name": "测试", "operator": "某某公司", "supportEmail": "a@example.com", "registrationOpen": true})
	m = byKey()
	if !m["llm"].OK || !m["operator"].OK {
		t.Fatalf("configured items should pass: %+v %+v", m["llm"], m["operator"])
	}
}
