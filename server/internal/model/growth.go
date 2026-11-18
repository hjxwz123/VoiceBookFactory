package model

import "time"

// ---------- Registration codes ----------
//
// When the site requires one (SiteSettings.RegCodeRequired), an account can only be created with a
// registration code issued by an administrator. A code is either single-use or shared (MaxUses 0 = no
// limit) and may expire. Registration codes are an access control; invite codes below are marketing.

const (
	RegCodeActive   = "active"
	RegCodeDisabled = "disabled"
)

type RegistrationCode struct {
	Base
	Code       string     `gorm:"size:40" json:"code"`          // display form
	Key        string     `gorm:"size:40;uniqueIndex" json:"-"` // normalized: upper case, letters and digits
	MaxUses    int        `json:"maxUses"`                      // 0 = unlimited
	Uses       int        `json:"uses"`
	ExpiresAt  *time.Time `json:"expiresAt"`
	Note       string     `gorm:"size:255" json:"note"`
	Batch      string     `gorm:"size:36;index" json:"batch"` // codes generated together share it
	Status     string     `gorm:"size:16;index;default:active" json:"status"`
	CreatedBy  string     `gorm:"size:36" json:"createdBy"`
	LastUsedAt *time.Time `json:"lastUsedAt"`
}

type RegistrationCodeUse struct {
	Base
	CodeID string `gorm:"size:36;index" json:"codeId"`
	UserID string `gorm:"size:36;uniqueIndex" json:"userId"`
	IP     string `gorm:"size:64" json:"ip"`
}

// ---------- Invitations between users (referral program) ----------
//
// Every user may get an invite code. Someone who signs up with it becomes their invitee: both can be
// rewarded with credits at sign-up, and the inviter earns a share of what the invitee pays, as set in
// ReferralSettings.

type InviteCode struct {
	Base
	UserID   string `gorm:"size:36;uniqueIndex" json:"userId"`
	Code     string `gorm:"size:20" json:"code"`
	Key      string `gorm:"size:20;uniqueIndex" json:"-"`
	Visits   int    `json:"visits"`
	Disabled bool   `json:"disabled"` // set by an administrator: the code no longer works
	Renamed  int    `json:"renamed"`  // times the owner chose their own code
}

type Referral struct {
	Base
	InviterID  string `gorm:"size:36;index" json:"inviterId"`
	InviteeID  string `gorm:"size:36;uniqueIndex" json:"inviteeId"`
	Code       string `gorm:"size:20" json:"code"`
	IP         string `gorm:"size:64" json:"ip"`
	SignupPaid bool   `json:"signupPaid"`  // the inviter's sign-up reward was granted
	Pending    bool   `json:"pending"`     // inviter's sign-up reward waits for the invitee's first payment
	Paid       int64  `json:"paidCents"`   // what the invitee has paid, in fen
	Earned     int64  `json:"earned"`      // credits the inviter received from this invitee
	Payments   int    `json:"payments"`    // payments by the invitee
	Commission int    `json:"commissions"` // payments that earned the inviter a commission
}

const (
	RewardSignupInviter = "signup_inviter"
	RewardSignupInvitee = "signup_invitee"
	RewardCommission    = "commission"
)

type ReferralReward struct {
	Base
	ReferralID string  `gorm:"size:36;index" json:"referralId"`
	UserID     string  `gorm:"size:36;index" json:"userId"` // who received it
	OtherID    string  `gorm:"size:36" json:"otherId"`      // the other side of the referral
	StudioID   string  `gorm:"size:36" json:"studioId"`
	Kind       string  `gorm:"size:20;index" json:"kind"`
	Credits    int64   `json:"credits"`
	PaymentID  string  `gorm:"size:36" json:"paymentId"`
	Amount     int64   `json:"amountCents"` // payment amount, for commissions
	Rate       float64 `json:"rate"`        // percent, for commissions
	Note       string  `gorm:"size:255" json:"note"`
}

// ---------- Payments ----------
//
// A Payment records money a user paid: an administrator entering an offline payment, a priced redeem
// code being redeemed, or (when one is connected) a payment gateway. Commissions are based on it.

const (
	PaymentAdmin  = "admin"
	PaymentRedeem = "redeem"
	PaymentOrder  = "order"
)

type Payment struct {
	Base
	UserID     string `gorm:"size:36;index" json:"userId"`
	StudioID   string `gorm:"size:36" json:"studioId"`
	Amount     int64  `json:"amountCents"` // in fen
	Source     string `gorm:"size:16;index" json:"source"`
	RefID      string `gorm:"size:64;index" json:"refId"`
	Summary    string `gorm:"size:255" json:"summary"` // what was bought
	OperatorID string `gorm:"size:36" json:"operatorId"`
}
