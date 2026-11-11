package model

import "time"

// Platform (admin console) roles. A user may hold several.
const (
	AdminSuper    = "super_admin"
	AdminOperator = "operator"
	AdminAuditor  = "auditor"
	AdminFinance  = "finance"
	AdminOps      = "ops"
)

const (
	UserActive   = "active"
	UserDisabled = "disabled"
)

type UserPreferences struct {
	Theme         string          `json:"theme"`  // light | dark | system
	Locale        string          `json:"locale"` // zh-CN | en
	Notify        map[string]bool `json:"notify"` // event type -> email enabled
	Onboarded     bool            `json:"onboarded"`
	Identity      string          `json:"identity"` // creator | studio | cv | enterprise
	DismissedTips []string        `json:"dismissedTips"`
}

type CVProfile struct {
	Enabled bool     `json:"enabled"`
	Tags    []string `json:"tags"`
	Intro   string   `json:"intro"`
	Works   []string `json:"works"`
	DemoIDs []string `json:"demoIds"` // asset ids
}

type User struct {
	Base
	Email          *string               `gorm:"size:191;uniqueIndex" json:"email"`
	Phone          *string               `gorm:"size:32;uniqueIndex" json:"phone"`
	PasswordHash   string                `gorm:"size:255" json:"-"`
	Nickname       string                `gorm:"size:64" json:"nickname"`
	AvatarURL      string                `gorm:"size:512" json:"avatarUrl"`
	Bio            string                `gorm:"size:500" json:"bio"`
	AdminRoles     StringList            `json:"adminRoles"`
	Status         string                `gorm:"size:16;default:active;index" json:"status"`
	EmailVerified  bool                  `json:"emailVerified"`
	LastLoginAt    *time.Time            `json:"lastLoginAt"`
	FailedLogins   int                   `json:"-"`
	LockedUntil    *time.Time            `json:"-"`
	Preferences    JSON[UserPreferences] `json:"preferences"`
	CV             JSON[CVProfile]       `json:"cv"`
	RealNameStatus string                `gorm:"size:16;default:none" json:"realNameStatus"` // none|pending|verified|rejected
	RealName       string                `gorm:"size:64" json:"-"`
	IDNumberMasked string                `gorm:"size:32" json:"-"`
	RealNameNote   string                `gorm:"size:255" json:"realNameNote"`
	DeletionAt     *time.Time            `json:"deletionAt"` // scheduled account deletion (7-day cool-off)
	PersonalStudio string                `gorm:"size:36" json:"personalStudioId"`
	LegalVersion   string                `gorm:"size:191" json:"legalVersion"` // version of the agreements the user accepted
	LegalAgreedAt  *time.Time            `json:"legalAgreedAt"`
	Extra          Map                   `json:"-"`
}

func (u *User) IsAdmin() bool { return len(u.AdminRoles.V) > 0 }

func (u *User) HasAdminRole(roles ...string) bool {
	for _, r := range u.AdminRoles.V {
		if r == AdminSuper {
			return true
		}
		for _, want := range roles {
			if r == want {
				return true
			}
		}
	}
	return false
}

type Session struct {
	Base
	UserID      string     `gorm:"size:36;index" json:"userId"`
	Kind        string     `gorm:"size:16" json:"kind"` // studio | admin
	RefreshHash string     `gorm:"size:64;uniqueIndex" json:"-"`
	UserAgent   string     `gorm:"size:255" json:"userAgent"`
	IP          string     `gorm:"size:64" json:"ip"`
	ExpiresAt   time.Time  `json:"expiresAt"`
	RevokedAt   *time.Time `json:"revokedAt"`
	LastUsedAt  time.Time  `json:"lastUsedAt"`
}

type VerificationCode struct {
	Base
	Target   string     `gorm:"size:191;index" json:"target"`
	Purpose  string     `gorm:"size:32;index" json:"purpose"` // register | reset | bind | login
	CodeHash string     `gorm:"size:64" json:"-"`
	Attempts int        `json:"-"`
	Expires  time.Time  `json:"expires"`
	UsedAt   *time.Time `json:"usedAt"`
}

// ---------- Studios ----------

const (
	RoleOwner    = "owner"
	RoleAdmin    = "admin"
	RoleProducer = "producer"
	RoleMember   = "member"
	RoleGuest    = "guest"
)

type PauseRules struct {
	SameSpeakerMs   int `json:"sameSpeakerMs"`
	SpeakerChangeMs int `json:"speakerChangeMs"`
	ParagraphMs     int `json:"paragraphMs"`
	SceneMs         int `json:"sceneMs"`
	ChapterHeadMs   int `json:"chapterHeadMs"`
	ChapterTailMs   int `json:"chapterTailMs"`
}

func DefaultPauseRules() PauseRules {
	return PauseRules{SameSpeakerMs: 300, SpeakerChangeMs: 500, ParagraphMs: 800, SceneMs: 1500, ChapterHeadMs: 1000, ChapterTailMs: 2000}
}

type StudioSettings struct {
	JoinApproval    bool       `json:"joinApproval"`
	GuestCountsSeat bool       `json:"guestCountsSeat"`
	PreferBYOK      bool       `json:"preferByok"`
	DefaultPreset   string     `json:"defaultPreset"`  // export preset key
	DefaultFormat   string     `json:"defaultFormat"`  // mp3 | wav | m4a
	DefaultBitrate  int        `json:"defaultBitrate"` // kbps
	Pause           PauseRules `json:"pause"`
	IntroTemplateID string     `json:"introTemplateId"`
	OutroTemplateID string     `json:"outroTemplateId"`
	WorkflowID      string     `json:"workflowId"`
	SpeechRate      int        `json:"speechRate"` // chars per minute for estimates
	AutoQC          bool       `json:"autoQc"`
	ProductionMode  bool       `json:"productionMode"` // enable tasks/workflow features by default
	MonologueReverb bool       `json:"monologueReverb"`
	DuckingDb       float64    `json:"duckingDb"`
}

func DefaultStudioSettings() StudioSettings {
	return StudioSettings{
		DefaultPreset: "generic", DefaultFormat: "mp3", DefaultBitrate: 192,
		Pause: DefaultPauseRules(), SpeechRate: 220, AutoQC: true, MonologueReverb: true, DuckingDb: -12,
	}
}

type Studio struct {
	Base
	Name           string               `gorm:"size:64" json:"name"`
	Type           string               `gorm:"size:16;index" json:"type"` // personal | team
	OwnerID        string               `gorm:"size:36;index" json:"ownerId"`
	Logo           string               `gorm:"size:512" json:"logo"`
	Description    string               `gorm:"size:500" json:"description"`
	PlanID         string               `gorm:"size:36" json:"planId"`         // plan in effect (owner's membership or override)
	PlanOverrideID string               `gorm:"size:36" json:"planOverrideId"` // contract plan pinned by an administrator
	PlanRenewAt    *time.Time           `json:"planRenewAt"`
	Frozen         int64                `json:"frozenCredits"` // credits on hold for running jobs
	SeatLimit      int                  `json:"seatLimit"`     // 0 = follow plan
	StorageLimit   int64                `json:"storageLimit"`  // bytes, 0 = follow plan
	StorageUsed    int64                `json:"storageUsed"`
	Status         string               `gorm:"size:16;default:active" json:"status"` // active | frozen
	Settings       JSON[StudioSettings] `json:"settings"`
	Features       StringList           `json:"features"` // extra features enabled by admin
	DeletedAt      *time.Time           `gorm:"index" json:"deletedAt"`
}

type StudioMember struct {
	Base
	StudioID  string `gorm:"size:36;uniqueIndex:idx_studio_user" json:"studioId"`
	UserID    string `gorm:"size:36;uniqueIndex:idx_studio_user;index" json:"userId"`
	Role      string `gorm:"size:16" json:"role"`
	Status    string `gorm:"size:16;default:active" json:"status"` // active | pending
	CreditCap int64  `json:"creditCap"`                            // monthly cap, 0 = unlimited
	Message   string `gorm:"size:255" json:"message"`              // join request message
}

type Invitation struct {
	Base
	StudioID  string     `gorm:"size:36;index" json:"studioId"`
	BookID    string     `gorm:"size:36" json:"bookId"` // guest invite into a single book
	Target    string     `gorm:"size:191" json:"target"`
	Role      string     `gorm:"size:16" json:"role"`
	Positions StringList `json:"positions"`
	Token     string     `gorm:"size:64;uniqueIndex" json:"token"`
	InvitedBy string     `gorm:"size:36" json:"invitedBy"`
	ExpiresAt time.Time  `json:"expiresAt"`
	MaxUses   int        `json:"maxUses"`
	Uses      int        `json:"uses"`
	RevokedAt *time.Time `json:"revokedAt"`
}

// OAuthIdentity links a third-party sign-in (an OAuth 2.0 / OpenID Connect provider set up in the
// admin console) to a user. Provider is the provider's id there; Subject is the provider's user id.
type OAuthIdentity struct {
	Base
	Provider    string     `gorm:"size:16;uniqueIndex:idx_oauth_sub" json:"provider"`
	Subject     string     `gorm:"size:128;uniqueIndex:idx_oauth_sub" json:"subject"`
	UserID      string     `gorm:"size:36;index" json:"userId"`
	Name        string     `gorm:"size:64" json:"name"`
	Email       string     `gorm:"size:191" json:"email"`
	Avatar      string     `gorm:"size:512" json:"avatar"`
	LastLoginAt *time.Time `json:"lastLoginAt"`
}
