package api

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"voicebook/internal/config"
	"voicebook/internal/core"
	"voicebook/internal/db/dbtest"
	"voicebook/internal/model"
)

// The analytics queries run on both databases (PostgreSQL in CI) and line their series up with the days.
func TestAnalyticsQueries(t *testing.T) {
	db := dbtest.Open(t)
	a := core.New(&config.Config{Workers: 1}, db, nil)
	s := &Server{app: a}
	u := &model.User{Nickname: "甲"}
	db.Create(u)
	db.Create(&model.UsageRecord{StudioID: "s1", UserID: u.ID, Feature: core.FeatTTS, Quantity: 1200, Credits: 120, CostCNY: 0.5, Tier: "standard"})
	db.Create(&model.UsageRecord{StudioID: "s1", UserID: u.ID, Feature: core.FeatScript, Quantity: 3000, Credits: 30, CostCNY: 0.1, Base: model.Base{CreatedAt: time.Now().AddDate(0, 0, -40)}})
	db.Create(&model.Payment{UserID: u.ID, Amount: 990, Source: model.PaymentAdmin})
	db.Create(&model.Job{Type: "render", Status: model.JobFailed})
	for _, days := range []string{"7", "30", "90"} {
		w := httptest.NewRecorder()
		h(s.analytics)(w, httptest.NewRequest("GET", "/api/admin/v1/dashboard/analytics?days="+days, nil))
		var out struct {
			Days    int      `json:"days"`
			DayList []string `json:"dayList"`
			Growth  struct {
				NewUsers struct{ Cur float64 } `json:"newUsers"`
				Series   struct {
					NewUsers []float64 `json:"newUsers"`
				} `json:"series"`
				Funnel []struct{ Value float64 } `json:"funnel"`
			} `json:"growth"`
			Revenue struct {
				Amount struct{ Cur float64 } `json:"amount"`
			} `json:"revenue"`
			AI struct {
				ByFeature []map[string]any `json:"byFeature"`
			} `json:"ai"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || w.Code != 200 {
			t.Fatalf("days=%s: %d %v %.300s", days, w.Code, err, w.Body.String())
		}
		if len(out.DayList) != out.Days || len(out.Growth.Series.NewUsers) != out.Days {
			t.Fatalf("days=%s: %d days, series %d", days, len(out.DayList), len(out.Growth.Series.NewUsers))
		}
		if out.Growth.NewUsers.Cur != 1 || out.Revenue.Amount.Cur != 990 || out.Growth.Funnel[4].Value != 1 {
			t.Fatalf("days=%s: totals %+v", days, out)
		}
		want := 1
		if days == "90" {
			want = 2 // the script usage 40 days ago
		}
		if len(out.AI.ByFeature) != want {
			t.Fatalf("days=%s: features %v", days, out.AI.ByFeature)
		}
	}
}
