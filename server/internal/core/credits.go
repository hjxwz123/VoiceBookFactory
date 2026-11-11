package core

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"voicebook/internal/model"
)

// Features used for metering.
const (
	FeatScript    = "script"
	FeatCharacter = "character"
	FeatLexicon   = "lexicon"
	FeatCasting   = "casting"
	FeatTTS       = "tts"
	FeatPreview   = "preview"
	FeatDesign    = "voice_design"
	FeatClone     = "voice_clone"
	FeatSFX       = "sfx"
	FeatMusic     = "music"
	FeatImage     = "image"
	FeatAlign     = "asr_align"
	FeatReview    = "asr_review"
	FeatModerate  = "moderation"
	FeatMarket    = "market"
	FeatQuick     = "quick_tts"
)

var FeatureNames = map[string]string{
	FeatScript: "智能画本", FeatCharacter: "角色分析", FeatLexicon: "发音词条提取", FeatCasting: "AI 选角",
	FeatTTS: "AI 配音", FeatPreview: "试听", FeatDesign: "AI 设计音色", FeatClone: "声音克隆", FeatSFX: "AI 音效",
	FeatMusic: "AI 配乐", FeatImage: "AI 封面", FeatAlign: "智能对轨", FeatReview: "智能审听", FeatModerate: "内容审核",
	FeatMarket: "授权音色", FeatQuick: "快速配音",
}

type Balance struct {
	Total     int64 `json:"total"`
	Frozen    int64 `json:"frozen"`
	Available int64 `json:"available"`
	Expiring  int64 `json:"expiring"` // within 30 days
}

func (a *App) Balance(studioID string) Balance {
	var total, expiring int64
	t := now()
	a.DB.Model(&model.CreditLot{}).Where("studio_id = ? AND remaining > 0 AND (expires_at IS NULL OR expires_at > ?)", studioID, t).
		Select("COALESCE(SUM(remaining),0)").Scan(&total)
	a.DB.Model(&model.CreditLot{}).Where("studio_id = ? AND remaining > 0 AND expires_at > ? AND expires_at < ?", studioID, t, t.Add(30*24*time.Hour)).
		Select("COALESCE(SUM(remaining),0)").Scan(&expiring)
	var st model.Studio
	a.DB.Select("frozen").First(&st, "id = ?", studioID)
	avail := total - st.Frozen
	if avail < 0 {
		avail = 0
	}
	return Balance{Total: total, Frozen: st.Frozen, Available: avail, Expiring: expiring}
}

// Grant adds credits as a new lot.
func (a *App) Grant(studioID string, amount int64, source string, expires *time.Time, note, operatorID string) error {
	if amount <= 0 {
		return Bad("积分数量必须大于 0")
	}
	return a.DB.Transaction(func(tx *gorm.DB) error {
		lot := model.CreditLot{StudioID: studioID, Source: source, Amount: amount, Remaining: amount, ExpiresAt: expires, Note: note}
		if err := tx.Create(&lot).Error; err != nil {
			return err
		}
		bal := a.balanceTx(tx, studioID)
		kind := "grant"
		switch source {
		case "plan":
			kind = "plan"
		case "refund":
			kind = "refund"
		case "market":
			kind = "market_income"
		case "promo":
			kind = "promo"
		}
		return tx.Create(&model.CreditLedger{StudioID: studioID, Delta: amount, BalanceAfter: bal, Kind: kind, Reason: note, OperatorID: operatorID, RefType: "lot", RefID: lot.ID}).Error
	})
}

func (a *App) balanceTx(tx *gorm.DB, studioID string) int64 {
	var total int64
	tx.Model(&model.CreditLot{}).Where("studio_id = ? AND remaining > 0 AND (expires_at IS NULL OR expires_at > ?)", studioID, now()).
		Select("COALESCE(SUM(remaining),0)").Scan(&total)
	return total
}

// Deduct removes credits (admin adjustment) without metering.
func (a *App) Deduct(studioID string, amount int64, note, operatorID string) error {
	return a.DB.Transaction(func(tx *gorm.DB) error {
		if err := a.consumeLots(tx, studioID, amount); err != nil {
			return err
		}
		return tx.Create(&model.CreditLedger{StudioID: studioID, Delta: -amount, BalanceAfter: a.balanceTx(tx, studioID), Kind: "adjust", Reason: note, OperatorID: operatorID}).Error
	})
}

func (a *App) consumeLots(tx *gorm.DB, studioID string, amount int64) error {
	var lots []model.CreditLot
	q := tx
	if tx.Dialector.Name() != "sqlite" { // SQLite serializes writers already
		q = tx.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	q.Where("studio_id = ? AND remaining > 0 AND (expires_at IS NULL OR expires_at > ?)", studioID, now()).
		Order("CASE WHEN expires_at IS NULL THEN 1 ELSE 0 END, expires_at asc, created_at asc").Find(&lots)
	left := amount
	for i := range lots {
		if left == 0 {
			break
		}
		take := lots[i].Remaining
		if take > left {
			take = left
		}
		if err := tx.Model(&lots[i]).Update("remaining", lots[i].Remaining-take).Error; err != nil {
			return err
		}
		left -= take
	}
	if left > 0 {
		return PaymentRequired("积分余额不足", nil)
	}
	return nil
}

// PriceRule returns the pricing rule for feature + tier (falls back to "*").
func (a *App) PriceRule(feature, tier string) model.PricingRule {
	var r model.PricingRule
	if tier != "" && a.DB.First(&r, "feature = ? AND tier = ?", feature, tier).Error == nil {
		return r
	}
	if a.DB.First(&r, "feature = ? AND tier = ?", feature, "*").Error == nil {
		return r
	}
	return model.PricingRule{Feature: feature, Tier: "*", Unit: "per_call", Credits: 0}
}

// Cost computes credits for a quantity under a rule.
func Cost(r model.PricingRule, qty float64) int64 {
	switch r.Unit {
	case "per_1k_chars", "per_1k_tokens":
		return int64(math.Ceil(qty / 1000 * float64(r.Credits)))
	case "per_minute":
		return int64(math.Ceil(qty / 60 * float64(r.Credits))) // qty in seconds
	case "per_second":
		return int64(math.Ceil(qty * float64(r.Credits)))
	default:
		if qty <= 0 {
			qty = 1
		}
		return int64(math.Ceil(qty)) * r.Credits
	}
}

func (a *App) Estimate(feature, tier string, qty float64) int64 {
	return Cost(a.PriceRule(feature, tier), qty)
}

type Charge struct {
	StudioID  string
	UserID    string
	BookID    string
	ChapterID string
	JobID     string
	Feature   string
	Tier      string
	Quantity  float64
	Unit      string
	Provider  string
	AccountID string
	Model     string
	BYOK      bool
	CostCNY   float64
	Extra     int64 // e.g. licensed voice surcharge
	Note      string
	Free      bool // cache hit / free regeneration
}

var holdMu sync.Mutex

// ChargeCredits meters usage and consumes credits. Returns the credits charged.
func (a *App) ChargeCredits(c Charge) (int64, error) {
	rule := a.PriceRule(c.Feature, c.Tier)
	credits := Cost(rule, c.Quantity)
	if c.BYOK {
		fee := a.Billing().BYOKFeePer1K
		credits = int64(math.Ceil(c.Quantity / 1000 * float64(fee)))
	}
	if c.Free {
		credits = 0
	}
	if !c.Free && rule.FreeDaily > 0 && c.UserID != "" {
		var used int64
		start := now().Truncate(24 * time.Hour)
		a.DB.Model(&model.UsageRecord{}).Where("user_id = ? AND feature = ? AND created_at >= ?", c.UserID, c.Feature, start).Count(&used)
		if used < int64(rule.FreeDaily) {
			credits = 0
		}
	}
	if credits > 0 && !c.BYOK {
		credits = discounted(credits, a.StudioPlan(c.StudioID).CreditRate)
	}
	credits += c.Extra // licensed-voice surcharges go to the voice owner and are never discounted
	if credits > 0 {
		if err := a.checkCaps(c.UserID, c.StudioID, c.BookID, credits); err != nil {
			return 0, err
		}
	}
	err := a.DB.Transaction(func(tx *gorm.DB) error {
		if credits > 0 {
			holdMu.Lock()
			defer holdMu.Unlock()
			// credits held for this job may be used; otherwise only the unheld balance is available
			var hold int64
			if c.JobID != "" {
				var j model.Job
				if tx.Select("credits_frozen").First(&j, "id = ?", c.JobID).Error == nil {
					hold = j.CreditsFrozen
				}
			}
			var st model.Studio
			tx.Select("frozen").First(&st, "id = ?", c.StudioID)
			avail := a.balanceTx(tx, c.StudioID) - st.Frozen + hold
			if avail < credits {
				return PaymentRequired(fmt.Sprintf("积分不足：本次需要 %d 积分，当前可用 %d 积分", credits, max(avail, 0)), map[string]any{"need": credits, "available": max(avail, 0)})
			}
			if err := a.consumeLots(tx, c.StudioID, credits); err != nil {
				return err
			}
			if hold > 0 {
				use := min(hold, credits)
				tx.Model(&model.Job{}).Where("id = ?", c.JobID).Update("credits_frozen", gorm.Expr("credits_frozen - ?", use))
				tx.Model(&model.Studio{}).Where("id = ?", c.StudioID).Update("frozen", gorm.Expr("CASE WHEN frozen > ? THEN frozen - ? ELSE 0 END", use, use))
			}
			if err := tx.Create(&model.CreditLedger{
				StudioID: c.StudioID, UserID: c.UserID, BookID: c.BookID, Delta: -credits, BalanceAfter: a.balanceTx(tx, c.StudioID),
				Kind: "consume", Feature: c.Feature, Reason: c.Note, RefType: "job", RefID: c.JobID,
			}).Error; err != nil {
				return err
			}
		}
		return tx.Create(&model.UsageRecord{
			StudioID: c.StudioID, UserID: c.UserID, BookID: c.BookID, ChapterID: c.ChapterID, JobID: c.JobID,
			Feature: c.Feature, Provider: c.Provider, AccountID: c.AccountID, Model: c.Model, Quantity: c.Quantity,
			Unit: c.Unit, Credits: credits, CostCNY: c.CostCNY, BYOK: c.BYOK, Tier: c.Tier,
		}).Error
	})
	if err != nil {
		return 0, err
	}
	if credits > 0 && c.JobID != "" {
		a.DB.Model(&model.Job{}).Where("id = ?", c.JobID).Update("credits_used", gorm.Expr("credits_used + ?", credits))
	}
	if c.AccountID != "" {
		a.DB.Model(&model.ProviderAccount{}).Where("id = ?", c.AccountID).Update("calls", gorm.Expr("calls + 1"))
	}
	return credits, nil
}

// discounted applies a plan's member rate (percent of list price; 0 or >=100 = full price).
func discounted(credits int64, rate int) int64 {
	if rate <= 0 || rate >= 100 {
		return credits
	}
	return max(int64(math.Ceil(float64(credits)*float64(rate)/100)), 1)
}

// Hold reserves credits for a job so concurrent work cannot overdraw the balance.
func (a *App) Hold(jobID, studioID string, amount int64) error {
	if amount <= 0 {
		return nil
	}
	holdMu.Lock()
	defer holdMu.Unlock()
	b := a.Balance(studioID)
	if b.Available < amount {
		return PaymentRequired(fmt.Sprintf("积分不足：预计需要 %d 积分，当前可用 %d 积分", amount, b.Available), map[string]any{"need": amount, "available": b.Available})
	}
	return a.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.Studio{}).Where("id = ?", studioID).Update("frozen", gorm.Expr("frozen + ?", amount)).Error; err != nil {
			return err
		}
		return tx.Model(&model.Job{}).Where("id = ?", jobID).Update("credits_frozen", gorm.Expr("credits_frozen + ?", amount)).Error
	})
}

func (a *App) releaseHold(jobID string) {
	holdMu.Lock()
	defer holdMu.Unlock()
	var j model.Job
	if a.DB.Select("id", "studio_id", "credits_frozen").First(&j, "id = ?", jobID).Error != nil || j.CreditsFrozen <= 0 {
		return
	}
	a.DB.Transaction(func(tx *gorm.DB) error {
		tx.Model(&model.Studio{}).Where("id = ?", j.StudioID).Update("frozen", gorm.Expr("CASE WHEN frozen > ? THEN frozen - ? ELSE 0 END", j.CreditsFrozen, j.CreditsFrozen))
		return tx.Model(&model.Job{}).Where("id = ?", jobID).Update("credits_frozen", 0).Error
	})
}

// checkCaps enforces member monthly caps and book budgets.
func (a *App) checkCaps(userID, studioID, bookID string, credits int64) error {
	monthStart := time.Date(now().Year(), now().Month(), 1, 0, 0, 0, 0, time.UTC)
	if userID != "" {
		var m model.StudioMember
		if a.DB.First(&m, "studio_id = ? AND user_id = ?", studioID, userID).Error == nil && m.CreditCap > 0 {
			var used int64
			a.DB.Model(&model.UsageRecord{}).Where("studio_id = ? AND user_id = ? AND created_at >= ?", studioID, userID, monthStart).Select("COALESCE(SUM(credits),0)").Scan(&used)
			if used+credits > m.CreditCap {
				return Forbidden(fmt.Sprintf("已达到本月个人积分上限（%d），请联系工作室管理员调整", m.CreditCap))
			}
		}
	}
	if bookID != "" {
		var b model.Book
		if a.DB.Select("budget").First(&b, "id = ?", bookID).Error == nil && b.Budget > 0 {
			var used int64
			a.DB.Model(&model.UsageRecord{}).Where("book_id = ?", bookID).Select("COALESCE(SUM(credits),0)").Scan(&used)
			if used+credits > b.Budget {
				return Forbidden(fmt.Sprintf("已超出本书积分预算（%d），请在图书设置中调整预算", b.Budget))
			}
		}
	}
	return nil
}

// ExpireLots zeroes expired lots and records ledger entries.
func (a *App) ExpireLots() {
	var lots []model.CreditLot
	a.DB.Where("remaining > 0 AND expires_at IS NOT NULL AND expires_at <= ?", now()).Find(&lots)
	for _, l := range lots {
		a.DB.Transaction(func(tx *gorm.DB) error {
			tx.Model(&l).Update("remaining", 0)
			return tx.Create(&model.CreditLedger{StudioID: l.StudioID, Delta: -l.Remaining, BalanceAfter: a.balanceTx(tx, l.StudioID), Kind: "expire", Reason: "积分到期"}).Error
		})
	}
}

// Allow permits `perMinute` events per key, counted across every instance when Redis is configured.
func (a *App) Allow(key string, perMinute int) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return a.Bus.Allow(ctx, key, perMinute)
}

// TransferCredits moves credits between two studios the user manages (e.g. from the studio receiving
// membership credits to a team studio). Each moved portion keeps its original expiry date.
func (a *App) TransferCredits(x *Access, toStudioID string, amount int64) error {
	if !x.IsManager() {
		return Forbidden("只有所有者或管理员可以划转积分")
	}
	if amount <= 0 {
		return Bad("请输入要划转的积分数量")
	}
	if toStudioID == x.StudioID {
		return Bad("不能划转到同一个工作室")
	}
	to, err := a.StudioAccess(x.User, toStudioID)
	if err != nil || !to.IsManager() {
		return Bad("只能划转到你管理的工作室")
	}
	if bal := a.Balance(x.StudioID); bal.Available < amount {
		return PaymentRequired(fmt.Sprintf("可用积分不足：当前可用 %d 积分", bal.Available), map[string]any{"available": bal.Available})
	}
	var fromName, toName string
	a.DB.Model(&model.Studio{}).Select("name").Where("id = ?", x.StudioID).Scan(&fromName)
	a.DB.Model(&model.Studio{}).Select("name").Where("id = ?", toStudioID).Scan(&toName)
	return a.DB.Transaction(func(tx *gorm.DB) error {
		var lots []model.CreditLot
		tx.Where("studio_id = ? AND remaining > 0 AND (expires_at IS NULL OR expires_at > ?)", x.StudioID, now()).
			Order("CASE WHEN expires_at IS NULL THEN 1 ELSE 0 END, expires_at asc, created_at asc").Find(&lots)
		left := amount
		for _, l := range lots {
			if left == 0 {
				break
			}
			take := min(l.Remaining, left)
			if err := tx.Model(&model.CreditLot{}).Where("id = ?", l.ID).Update("remaining", l.Remaining-take).Error; err != nil {
				return err
			}
			if err := tx.Create(&model.CreditLot{StudioID: toStudioID, Source: "transfer", Amount: take, Remaining: take, ExpiresAt: l.ExpiresAt, Note: "来自「" + fromName + "」"}).Error; err != nil {
				return err
			}
			left -= take
		}
		if left > 0 {
			return PaymentRequired("可用积分不足", nil)
		}
		if err := tx.Create(&model.CreditLedger{StudioID: x.StudioID, UserID: x.User.ID, Delta: -amount, BalanceAfter: a.balanceTx(tx, x.StudioID), Kind: "transfer", Reason: "划转到「" + toName + "」", RefType: "studio", RefID: toStudioID}).Error; err != nil {
			return err
		}
		return tx.Create(&model.CreditLedger{StudioID: toStudioID, UserID: x.User.ID, Delta: amount, BalanceAfter: a.balanceTx(tx, toStudioID), Kind: "transfer", Reason: "来自「" + fromName + "」", RefType: "studio", RefID: x.StudioID}).Error
	})
}
