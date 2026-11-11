package core

import (
	"strings"
	"testing"

	"voicebook/internal/model"
)

func TestLegalDocumentsAndConsent(t *testing.T) {
	a, _ := testApp(t)
	a.seedLegal()
	a.seedLegal() // idempotent
	var n int64
	a.DB.Model(&model.Article{}).Where("kind = ?", "legal").Count(&n)
	if n != 4 {
		t.Fatalf("want 4 legal documents, got %d", n)
	}
	v := a.LegalVersion()
	if !strings.Contains(v, "terms:") || !strings.Contains(v, "privacy:") || !strings.Contains(v, "copyright:") {
		t.Fatalf("version must cover the required documents: %q", v)
	}

	// placeholders become the operator details from the site settings
	a.SetSetting("site", map[string]any{"name": "测试书坊", "operator": "某某文化有限公司", "supportEmail": "legal@example.com", "registrationOpen": true})
	out := a.RenderLegal("{平台名称}由{运营主体}运营，联系 {联系邮箱}")
	if out != "测试书坊由某某文化有限公司运营，联系 legal@example.com" {
		t.Fatalf("rendered %q", out)
	}

	// sign-up needs explicit consent and records the accepted version
	if _, err := a.Register(RegisterInput{Email: "a@example.com", Password: "Passw0rd!"}); err == nil {
		t.Fatal("registering without accepting the agreements must fail")
	}
	u, err := a.Register(RegisterInput{Email: "b@example.com", Password: "Passw0rd!", Agree: true})
	if err != nil {
		t.Fatal(err)
	}
	if u.LegalVersion != v || u.LegalAgreedAt == nil {
		t.Fatalf("consent not recorded: %q %v", u.LegalVersion, u.LegalAgreedAt)
	}

	// a new version of a required document asks everyone again
	a.DB.Model(&model.Article{}).Where("kind = ? AND slug = ?", "legal", LegalPrivacy).Update("version", "2027-01-01 10:00")
	if nv := a.LegalVersion(); nv == v {
		t.Fatal("publishing a new privacy policy must change the version")
	} else if got := a.AgreeLegal(u.ID); got != nv {
		t.Fatalf("agree recorded %q, want %q", got, nv)
	}
}
