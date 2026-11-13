package core

import (
	"strings"
	"testing"

	"voicebook/internal/model"
)

func TestAPIKeyScopes(t *testing.T) {
	for in, want := range map[string]string{
		"read,write":        "read,produce", // the dialog's old default
		"generate":          "produce,tts",  // the old "发起生成"
		"tts,read,nonsense": "read,tts",     // unknown dropped, fixed order
		"READ, produce":     "read,produce",
	} {
		if got := strings.Join(NormalizeScopes(strings.Split(in, ",")), ","); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
	old := &model.APIKey{Scopes: model.Strings("read", "write")}
	if !HasScope(old, ScopeProduce) || HasScope(old, ScopeTTS) {
		t.Fatal("a key made with the old names should produce but not voice passages")
	}
}

func TestManagedHelpDocs(t *testing.T) {
	a, _ := testApp(t)
	a.syncManagedDocs()
	var art model.Article
	if err := a.DB.First(&art, "kind = ? AND slug = ?", "help", OpenAPIDocSlug).Error; err != nil {
		t.Fatal("guide not added")
	}
	if !strings.Contains(art.Body, "X-Voicebook-Signature") || !art.Published {
		t.Fatalf("guide = %.80q", art.Body)
	}
	// a newer built-in text replaces an untouched article...
	old := managedDocs[0].Body
	t.Cleanup(func() { managedDocs[0].Body = old })
	managedDocs[0].Body = old + "\n\n新增一节。"
	a.syncManagedDocs()
	a.DB.First(&art, "id = ?", art.ID)
	if !strings.HasSuffix(art.Body, "新增一节。") {
		t.Fatal("untouched guide not updated")
	}
	// ...but never one an administrator edited
	a.DB.Model(&art).Update("body", "管理员改过的内容")
	managedDocs[0].Body = old
	a.syncManagedDocs()
	a.DB.First(&art, "id = ?", art.ID)
	if art.Body != "管理员改过的内容" {
		t.Fatal("an edited guide was overwritten")
	}
	var n int64
	a.DB.Model(&model.Article{}).Where("slug = ?", OpenAPIDocSlug).Count(&n)
	if n != 1 {
		t.Fatalf("%d copies of the guide", n)
	}
}

func TestMessageRecipients(t *testing.T) {
	a, u := testApp(t)
	email, phone := "Zhang@Example.com", "13800138000"
	a.DB.Model(u).Updates(map[string]any{"email": email, "phone": phone})
	other := &model.User{Nickname: "李四"}
	a.DB.Create(other)
	found, unknown := a.ResolveRecipients([]string{"zhang@example.com\n+8613800138000", other.ID + "，nobody@example.com、" + other.ID})
	if len(found) != 2 || found[0].ID != u.ID || found[1].ID != other.ID {
		t.Fatalf("found %+v", found)
	}
	if len(unknown) != 1 || unknown[0] != "nobody@example.com" {
		t.Fatalf("unknown %v", unknown)
	}
	// without email the message is an in-app notification for each user
	a.SendMessage([]string{u.ID, other.ID, u.ID}, "维护通知", "今晚 23 点维护", "/help", false)
	var n int64
	a.DB.Model(&model.Notification{}).Where("type = ? AND title = ?", "broadcast", "维护通知").Count(&n)
	if n != 2 {
		t.Fatalf("%d notifications", n)
	}
}
