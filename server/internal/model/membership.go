package model

import (
	"time"

	"gorm.io/gorm"
)

// ---------- Membership ----------
//
// A membership belongs to a user account. The plan in effect for a user is the highest-level plan among
// their current membership periods (the default plan when there is none). Every studio the user owns
// follows that plan unless an administrator pinned the studio to a contract plan (Studio.PlanOverrideID).
// Monthly plan credits are granted to one studio of the user's choice (UserMembership.CreditStudioID).

// UserMembership is the per-user summary kept in sync with MembershipPeriod rows.
type UserMembership struct {
	Base
	UserID         string     `gorm:"size:36;uniqueIndex" json:"userId"`
	PlanID         string     `gorm:"size:36;index" json:"planId"` // plan in effect now
	PeriodID       string     `gorm:"size:36" json:"periodId"`     // period providing it ("" = default plan)
	ExpiresAt      *time.Time `json:"expiresAt"`                   // when the current plan ends (nil = default or permanent)
	CreditStudioID string     `gorm:"size:36" json:"creditStudioId"`
	NextCreditAt   *time.Time `gorm:"index" json:"nextCreditAt"`
	NextCheckAt    *time.Time `gorm:"index" json:"-"`   // next period boundary to re-evaluate
	Reminded       string     `gorm:"size:64" json:"-"` // "<periodId>:<days>" of the last expiry reminder sent
	EverPaid       bool       `json:"everPaid"`         // had a non-default plan at some point (for new-user-only offers)
}

const (
	MemberSourceRedeem  = "redeem"
	MemberSourceAdmin   = "admin"
	MemberSourceTrial   = "trial"
	MemberSourceOrder   = "order"
	MemberSourceMigrate = "migrate"
)

// MembershipPeriod is one granted stretch of a plan. Periods of a higher-level plan take precedence;
// a lower plan that is active meanwhile is paused (its end is pushed back) and resumes afterwards.
type MembershipPeriod struct {
	Base
	UserID      string     `gorm:"size:36;index" json:"userId"`
	PlanID      string     `gorm:"size:36;index" json:"planId"`
	StartsAt    time.Time  `gorm:"index" json:"startsAt"`
	ExpiresAt   *time.Time `gorm:"index" json:"expiresAt"` // nil = permanent
	Days        int        `json:"days"`                   // days granted in total (0 = permanent)
	Source      string     `gorm:"size:16" json:"source"`
	RefID       string     `gorm:"size:36" json:"refId"` // redeem code / order
	Note        string     `gorm:"size:255" json:"note"`
	OperatorID  string     `gorm:"size:36" json:"operatorId"`
	Revoked     bool       `gorm:"index" json:"revoked"`
	ActivatedAt *time.Time `json:"activatedAt"` // first time it took effect (first-month credits delivered)
}

// ---------- Redeem codes ----------

const (
	RedeemMembership = "membership"
	RedeemCredits    = "credits"

	RedeemModeUnique = "unique" // N single-use codes
	RedeemModeShared = "shared" // one public code, many users

	CodeUnused   = "unused"
	CodeUsed     = "used"
	CodeDisabled = "disabled"
)

type RedeemBatch struct {
	Base
	Name         string     `gorm:"size:100" json:"name"`
	Kind         string     `gorm:"size:16;index" json:"kind"` // membership | credits
	PlanID       string     `gorm:"size:36" json:"planId"`
	Days         int        `json:"days"` // membership length, 0 = permanent
	Credits      int64      `json:"credits"`
	CreditDays   int        `json:"creditDays"` // validity of redeemed credits, 0 = never expire
	Mode         string     `gorm:"size:16" json:"mode"`
	Count        int        `json:"count"`        // codes generated (unique mode)
	MaxUses      int        `json:"maxUses"`      // shared mode: total redemptions, 0 = unlimited
	PerUserLimit int        `json:"perUserLimit"` // redemptions per user within this batch, 0 = unlimited
	NewUsersOnly bool       `json:"newUsersOnly"` // only accounts that never had a paid membership
	Prefix       string     `gorm:"size:8" json:"prefix"`
	StartsAt     *time.Time `json:"startsAt"`
	ExpiresAt    *time.Time `json:"expiresAt"`                    // last moment a code can be redeemed
	Channel      string     `gorm:"size:64;index" json:"channel"` // marketing channel for attribution
	Note         string     `gorm:"size:255" json:"note"`
	Status       string     `gorm:"size:16;index;default:active" json:"status"` // active | disabled
	Used         int        `json:"used"`
	CreatedBy    string     `gorm:"size:36" json:"createdBy"`
	Exported     int        `json:"exported"` // times exported
}

type RedeemCode struct {
	Base
	BatchID string     `gorm:"size:36;index" json:"batchId"`
	Code    string     `gorm:"size:40" json:"code"`          // display form, e.g. VIP-7KQ2-MX9P-4TJD
	Key     string     `gorm:"size:40;uniqueIndex" json:"-"` // normalized: upper case, no separators
	Status  string     `gorm:"size:16;index" json:"status"`  // unused | used | disabled
	Uses    int        `json:"uses"`
	UsedBy  string     `gorm:"size:36;index" json:"usedBy"` // last (unique mode: only) user
	UsedAt  *time.Time `json:"usedAt"`
}

type RedeemUse struct {
	Base
	BatchID  string `gorm:"size:36;index" json:"batchId"`
	CodeID   string `gorm:"size:36;index" json:"codeId"`
	UserID   string `gorm:"size:36;index" json:"userId"`
	StudioID string `gorm:"size:36" json:"studioId"` // credits target
	Kind     string `gorm:"size:16" json:"kind"`
	PlanID   string `gorm:"size:36" json:"planId"`
	Days     int    `json:"days"`
	Credits  int64  `json:"credits"`
	Summary  string `gorm:"size:255" json:"summary"`
	IP       string `gorm:"size:64" json:"ip"`
}

// AfterFind guarantees list fields are never null in API responses (older rows may hold NULL).
func (p *Plan) AfterFind(*gorm.DB) error {
	for _, l := range []*StringList{&p.Features, &p.Benefits, &p.ExportFormats} {
		if l.V == nil {
			l.V = []string{}
		}
	}
	return nil
}
