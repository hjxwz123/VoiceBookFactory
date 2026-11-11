package core

import (
	"strings"
	"testing"
	"time"

	"voicebook/internal/model"
)

func TestRealImageAccountBeatsPlaceholder(t *testing.T) {
	a, u := testApp(t)
	a.seedProviders() // built-in abstract generator, priority 1
	acc, _, err := a.genAccount(model.KindImage, u.PersonalStudio)
	if err != nil || acc.Provider != "placeholder" {
		t.Fatalf("with nothing configured the placeholder should answer, got %v %v", acc, err)
	}
	clock := now().Add(time.Minute) // added later, same default priority
	real := model.ProviderAccount{Kind: model.KindImage, Provider: "gemini_image", Scope: "platform", Name: "Nano Banana", Enabled: true, Priority: 1, Model: "gemini-nano-banana-2.1", Config: model.EmptyMap()}
	real.CreatedAt = clock
	a.DB.Create(&real)
	if acc, _, _ = a.genAccount(model.KindImage, u.PersonalStudio); acc.ID != real.ID {
		t.Fatalf("adding a real account at the default priority should be enough to switch over, got %s", acc.Provider)
	}
	// a routing rule can pin another model on the same account
	a.DB.Create(&model.RoutingRule{Feature: model.KindImage, Tier: "*", PrimaryID: real.ID, Model: "gemini-3-pro-image", Enabled: true})
	if acc, _, _ = a.genAccount(model.KindImage, u.PersonalStudio); acc.Model != "gemini-3-pro-image" {
		t.Fatalf("routing model override ignored: %s", acc.Model)
	}
	var stored model.ProviderAccount
	a.DB.First(&stored, "id = ?", real.ID)
	if stored.Model != "gemini-nano-banana-2.1" {
		t.Fatal("the override must not be written back to the account")
	}
}

func TestCoverImagePrompt(t *testing.T) {
	b := model.Book{Title: "长安十二时辰", Author: "马伯庸", Genre: "历史悬疑"}
	p := coverImagePrompt(b, "夜色中的长安城楼，灯火通明", true)
	for _, want := range []string{"1:1", "「长安十二时辰」", "「马伯庸」", "夜色中的长安城楼", "错别字", "安全边距"} {
		if !strings.Contains(p, want) {
			t.Errorf("cover brief misses %q:\n%s", want, p)
		}
	}
	if p := coverImagePrompt(b, "城楼", false); strings.Contains(p, "长安十二时辰") || !strings.Contains(p, "不要出现任何文字") {
		t.Errorf("without lettering the title must stay off the picture:\n%s", p)
	}
	id := endUserID("user-123")
	if id != endUserID("user-123") || strings.Contains(id, "user-123") || len(id) != 27 {
		t.Errorf("end-user id must be stable and opaque, got %s", id)
	}
}

func TestHelpDocUpgradesFromPreviousRelease(t *testing.T) {
	a, _ := testApp(t)
	a.DB.Where("kind = ?", "help").Delete(&model.Article{})
	a.DB.Create(&model.Article{Kind: "help", Title: "配置 AI 模型", Body: helpSuperseded["配置 AI 模型"][0], Published: true})
	a.syncHelpDocs()
	var art model.Article
	a.DB.Where("kind = ? AND title = ?", "help", "配置 AI 模型").First(&art)
	if !strings.Contains(art.Body, "Nano Banana") {
		t.Fatal("an untouched copy of the previous built-in text should be upgraded")
	}
	a.DB.Model(&art).Update("body", "运营改过")
	a.syncHelpDocs()
	a.DB.First(&art, "id = ?", art.ID)
	if art.Body != "运营改过" {
		t.Fatal("an edited article must be left alone")
	}
}
