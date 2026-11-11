package model

import "time"

// ---------- AI providers ----------

const (
	KindLLM   = "llm"
	KindTTS   = "tts"
	KindASR   = "asr"
	KindSFX   = "sfx"
	KindMusic = "music"
	KindImage = "image"

	// LLM wire protocols configurable per provider account.
	ProtoOpenAIChat      = "openai_chat"
	ProtoOpenAIResponses = "openai_responses"
	ProtoAnthropic       = "anthropic"
)

type ProviderModel struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Capabilities []string `json:"capabilities"`
	PriceTier    string   `json:"priceTier"`
}

type ProviderAccount struct {
	Base
	Kind           string                `gorm:"size:16;index" json:"kind"`
	Provider       string                `gorm:"size:32;index" json:"provider"`
	Scope          string                `gorm:"size:16;index" json:"scope"` // platform | studio
	StudioID       string                `gorm:"size:36;index" json:"studioId"`
	Name           string                `gorm:"size:64" json:"name"`
	BaseURL        string                `gorm:"size:255" json:"baseUrl"`
	CredentialsEnc string                `gorm:"type:text" json:"-"`
	KeyLast4       string                `gorm:"size:8" json:"keyLast4"`
	Model          string                `gorm:"size:128" json:"model"`
	Config         Map                   `json:"config"`
	Enabled        bool                  `json:"enabled"`
	Priority       int                   `json:"priority"`
	RPM            int                   `json:"rpm"`
	Concurrency    int                   `json:"concurrency"`
	CostPerUnit    float64               `json:"costPerUnit"` // platform cost in CNY per unit (1k chars / 1k tokens / minute)
	Models         JSON[[]ProviderModel] `json:"models"`
	HealthStatus   string                `gorm:"size:16" json:"healthStatus"` // ok | error | unknown
	HealthNote     string                `gorm:"size:500" json:"healthNote"`
	CheckedAt      *time.Time            `json:"checkedAt"`
	Calls          int64                 `json:"calls"`
	Failures       int64                 `json:"failures"`
	TotalLatencyMs int64                 `json:"totalLatencyMs"`
}

type RoutingRule struct {
	Base
	Feature   string     `gorm:"size:32;index" json:"feature"` // script | character | casting | lexicon | moderation | asr | sfx | music | voice_design | clone
	Tier      string     `gorm:"size:16" json:"tier"`          // * | economy | standard | premium
	StudioID  string     `gorm:"size:36;index" json:"studioId"`
	PrimaryID string     `gorm:"size:36" json:"primaryId"`
	Fallbacks StringList `json:"fallbacks"`
	Model     string     `gorm:"size:128" json:"model"`
	Enabled   bool       `json:"enabled"`
	Note      string     `gorm:"size:255" json:"note"`
}

// CastingPolicy maps a quality tier to preferred voice price tiers per character kind.
type CastingPolicy struct {
	Narrator   string `json:"narrator"`
	Lead       string `json:"lead"`
	Supporting string `json:"supporting"`
	Minor      string `json:"minor"`
	Crowd      string `json:"crowd"`
}

type PromptTemplate struct {
	Base
	Key       string `gorm:"size:64;index" json:"key"`
	Version   int    `json:"version"`
	Name      string `gorm:"size:100" json:"name"`
	Content   string `gorm:"type:text" json:"content"`
	Active    bool   `json:"active"`
	Note      string `gorm:"size:255" json:"note"`
	Rollout   int    `json:"rollout"` // 0-100 percent for gray release (100 = full)
	CreatedBy string `gorm:"size:36" json:"createdBy"`
}

type EvalItem struct {
	Text       string          `json:"text"`
	Characters []EvalCharacter `json:"characters"`
	Expected   []EvalLabel     `json:"expected"`
}

type EvalCharacter struct {
	Name    string   `json:"name"`
	Aliases []string `json:"aliases"`
	Gender  string   `json:"gender"`
}

type EvalLabel struct {
	Index   int    `json:"index"`
	Speaker string `json:"speaker"`
	Type    string `json:"type"`
	Emotion string `json:"emotion"`
}

type EvalSet struct {
	Base
	Name    string                 `gorm:"size:100" json:"name"`
	Kind    string                 `gorm:"size:32" json:"kind"`
	Items   JSON[[]EvalItem]       `json:"items"`
	LastRun Map                    `json:"lastRun"`
	History JSON[[]map[string]any] `json:"history"`
}

// ---------- Jobs ----------

const (
	JobQueued    = "queued"
	JobRunning   = "running"
	JobSucceeded = "succeeded"
	JobFailed    = "failed"
	JobCanceled  = "canceled"
	JobPaused    = "paused"
)

type Job struct {
	Base
	Type          string     `gorm:"size:32;index" json:"type"`
	Lane          string     `gorm:"size:16;index" json:"lane"`
	StudioID      string     `gorm:"size:36;index" json:"studioId"`
	BookID        string     `gorm:"size:36;index" json:"bookId"`
	ChapterID     string     `gorm:"size:36" json:"chapterId"`
	ParentID      string     `gorm:"size:36;index" json:"parentId"`
	RefType       string     `gorm:"size:32" json:"refType"`
	RefID         string     `gorm:"size:36;index" json:"refId"`
	Title         string     `gorm:"size:200" json:"title"`
	Status        string     `gorm:"size:16;index" json:"status"`
	Priority      int        `json:"priority"`
	Payload       Map        `json:"payload"`
	Result        Map        `json:"result"`
	Progress      float64    `json:"progress"`
	ProgressText  string     `gorm:"size:255" json:"progressText"`
	Attempts      int        `json:"attempts"`
	MaxAttempts   int        `json:"maxAttempts"`
	RunAt         time.Time  `gorm:"index" json:"runAt"`
	LockedBy      string     `gorm:"size:64" json:"-"`
	LockedUntil   *time.Time `json:"-"`
	Error         string     `gorm:"size:1000" json:"error"`
	CreatedBy     string     `gorm:"size:36;index" json:"createdBy"`
	StartedAt     *time.Time `json:"startedAt"`
	FinishedAt    *time.Time `json:"finishedAt"`
	CreditsFrozen int64      `json:"creditsFrozen"`
	CreditsUsed   int64      `json:"creditsUsed"`
	CancelReq     bool       `json:"cancelRequested"`
}

// ---------- Billing ----------

// Plan is a membership level. Limits use 0 for "unlimited" (or "follow the site setting").
type Plan struct {
	Base
	Code    string `gorm:"size:32;uniqueIndex" json:"code"`
	Name    string `gorm:"size:64" json:"name"`
	Tagline string `gorm:"size:200" json:"tagline"`
	Level   int    `json:"level"` // higher wins when a user holds several memberships at once

	// prices (cents, display only: payment is not integrated)
	PriceMonthly    int64 `json:"priceMonthly"`
	PriceYearly     int64 `json:"priceYearly"`
	OriginalMonthly int64 `json:"originalMonthly"` // struck-through price for promotions

	// credits come with the membership
	MonthlyCredits int64 `json:"monthlyCredits"`
	BonusCredits   int64 `json:"bonusCredits"` // one-off gift when the plan is activated
	CreditMonths   int   `json:"creditMonths"` // validity of monthly credits in months (0 = 2)
	CreditRate     int   `json:"creditRate"`   // % of list price charged per use (0 or 100 = full price, 90 = 10% off)

	// resource limits
	MaxStudios    int        `json:"maxStudios"`    // studios a member may own, personal one included
	Seats         int        `json:"seats"`         // members per studio
	MaxBooks      int        `json:"maxBooks"`      // books per studio
	StorageGB     int        `json:"storageGb"`     // storage per studio
	CloneVoices   int        `json:"cloneVoices"`   // cloned voices kept per studio
	MaxBookWords  int        `json:"maxBookWords"`  // words per book (0 = site setting)
	MaxUploadMB   int        `json:"maxUploadMb"`   // single upload size (0 = site setting)
	QueuePriority int        `json:"queuePriority"` // 0-10, higher runs first when the platform is busy
	ExportFormats StringList `json:"exportFormats"` // allowed export formats, empty = all

	// rights
	HighQuality bool       `json:"highQuality"` // premium-tier voices
	Commercial  bool       `json:"commercial"`
	Features    StringList `json:"features"`

	// presentation
	Benefits     StringList `json:"benefits"` // selling points shown on the plan card
	Badge        string     `gorm:"size:16" json:"badge"`
	Highlight    bool       `json:"highlight"`
	ContactSales bool       `json:"contactSales"`
	Listed       bool       `json:"listed"`
	SortOrder    int        `json:"sortOrder"`
}

const (
	FeatAlignment  = "alignment"
	FeatReview     = "review"
	FeatProduction = "production"
	FeatPayroll    = "payroll"
	FeatBYOK       = "byok"
	FeatOpenAPI    = "openapi"
	FeatBatch      = "batch"
	FeatPrivate    = "private"
	FeatProClone   = "pro_clone"
	FeatDistribute = "distribute"
	FeatStems      = "stems" // multitrack stem export
)

type CreditLot struct {
	Base
	StudioID  string     `gorm:"size:36;index" json:"studioId"`
	Source    string     `gorm:"size:16" json:"source"` // signup | plan | grant | purchase | market | promo | refund
	Amount    int64      `json:"amount"`
	Remaining int64      `json:"remaining"`
	ExpiresAt *time.Time `gorm:"index" json:"expiresAt"`
	Note      string     `gorm:"size:255" json:"note"`
}

type CreditLedger struct {
	Base
	StudioID     string `gorm:"size:36;index" json:"studioId"`
	UserID       string `gorm:"size:36;index" json:"userId"`
	BookID       string `gorm:"size:36;index" json:"bookId"`
	Delta        int64  `json:"delta"`
	BalanceAfter int64  `json:"balanceAfter"`
	Kind         string `gorm:"size:16;index" json:"kind"` // grant | plan | consume | refund | adjust | market_income | market_spend | promo | expire
	Feature      string `gorm:"size:32;index" json:"feature"`
	Reason       string `gorm:"size:255" json:"reason"`
	RefType      string `gorm:"size:32" json:"refType"`
	RefID        string `gorm:"size:36" json:"refId"`
	OperatorID   string `gorm:"size:36" json:"operatorId"`
}

type UsageRecord struct {
	Base
	StudioID  string  `gorm:"size:36;index" json:"studioId"`
	UserID    string  `gorm:"size:36;index" json:"userId"`
	BookID    string  `gorm:"size:36;index" json:"bookId"`
	ChapterID string  `gorm:"size:36" json:"chapterId"`
	JobID     string  `gorm:"size:36" json:"jobId"`
	Feature   string  `gorm:"size:32;index" json:"feature"`
	Provider  string  `gorm:"size:32" json:"provider"`
	AccountID string  `gorm:"size:36;index" json:"accountId"`
	Model     string  `gorm:"size:128" json:"model"`
	Quantity  float64 `json:"quantity"`
	Unit      string  `gorm:"size:16" json:"unit"` // chars | tokens | seconds | calls
	Credits   int64   `json:"credits"`
	CostCNY   float64 `json:"costCny"`
	BYOK      bool    `json:"byok"`
	Tier      string  `gorm:"size:16" json:"tier"`
}

type PricingRule struct {
	Base
	Feature   string `gorm:"size:32;index" json:"feature"`
	Tier      string `gorm:"size:16" json:"tier"` // * or price tier
	Unit      string `gorm:"size:16" json:"unit"` // per_1k_chars | per_call | per_minute
	Credits   int64  `json:"credits"`
	FreeDaily int    `json:"freeDaily"`
	Name      string `gorm:"size:64" json:"name"`
	Note      string `gorm:"size:255" json:"note"`
}

type PromoCode struct {
	Base
	Code      string     `gorm:"size:32;uniqueIndex" json:"code"`
	Credits   int64      `json:"credits"`
	MaxUses   int        `json:"maxUses"`
	Uses      int        `json:"uses"`
	ExpiresAt *time.Time `json:"expiresAt"`
	Enabled   bool       `json:"enabled"`
	Note      string     `gorm:"size:255" json:"note"`
}

type PromoRedemption struct {
	Base
	PromoID  string `gorm:"size:36;uniqueIndex:idx_promo_studio" json:"promoId"`
	StudioID string `gorm:"size:36;uniqueIndex:idx_promo_studio" json:"studioId"`
	UserID   string `gorm:"size:36" json:"userId"`
}

// ---------- Production management ----------

type WorkflowTemplate struct {
	Base
	Scope     string                `gorm:"size:16;index" json:"scope"` // system | studio
	StudioID  string                `gorm:"size:36;index" json:"studioId"`
	Name      string                `gorm:"size:64" json:"name"`
	Stages    JSON[[]WorkflowStage] `json:"stages"`
	IsDefault bool                  `json:"isDefault"`
	Note      string                `gorm:"size:255" json:"note"`
}

const (
	TaskPending    = "pending"
	TaskInProgress = "in_progress"
	TaskReviewing  = "reviewing"
	TaskDone       = "done"
	TaskRejected   = "rejected"
)

type ProductionTask struct {
	Base
	StudioID      string     `gorm:"size:36;index" json:"studioId"`
	BookID        string     `gorm:"size:36;index" json:"bookId"`
	ChapterID     string     `gorm:"size:36;index" json:"chapterId"`
	StageKey      string     `gorm:"size:32" json:"stageKey"`
	StageIndex    int        `json:"stageIndex"`
	StageName     string     `gorm:"size:64" json:"stageName"`
	StageKind     string     `gorm:"size:16" json:"stageKind"`
	CharacterID   string     `gorm:"size:36" json:"characterId"`
	AssigneeID    string     `gorm:"size:36;index" json:"assigneeId"`
	Status        string     `gorm:"size:16;index" json:"status"`
	DueAt         *time.Time `json:"dueAt"`
	Measure       int64      `json:"measure"`
	MeasureUnit   string     `gorm:"size:16" json:"measureUnit"` // words | minutes | episodes
	StartedAt     *time.Time `json:"startedAt"`
	SubmittedAt   *time.Time `json:"submittedAt"`
	CompletedAt   *time.Time `json:"completedAt"`
	RejectReason  string     `gorm:"size:500" json:"rejectReason"`
	ApprovedBy    string     `gorm:"size:36" json:"approvedBy"`
	Reworks       int        `json:"reworks"`
	Settled       bool       `json:"settled"`
	SettledAt     *time.Time `json:"settledAt"`
	RemindedAt    *time.Time `json:"-"`
	NeedsApproval bool       `json:"needsApproval"`
}

type PayRate struct {
	Base
	StudioID  string `gorm:"size:36;index" json:"studioId"`
	StageKey  string `gorm:"size:32" json:"stageKey"`
	StageKind string `gorm:"size:16" json:"stageKind"`
	Unit      string `gorm:"size:16" json:"unit"` // per_1k_words | per_minute | per_episode
	PriceCent int64  `json:"priceCent"`
	UserID    string `gorm:"size:36" json:"userId"` // optional override
	Note      string `gorm:"size:255" json:"note"`
}

type Comment struct {
	Base
	StudioID    string     `gorm:"size:36;index" json:"studioId"`
	BookID      string     `gorm:"size:36;index" json:"bookId"`
	ChapterID   string     `gorm:"size:36;index" json:"chapterId"`
	SegmentID   string     `gorm:"size:36;index" json:"segmentId"`
	AtMs        *int64     `json:"atMs"`
	Body        string     `gorm:"type:text" json:"body"`
	AuthorID    string     `gorm:"size:36" json:"authorId"`
	AuthorName  string     `gorm:"size:64" json:"authorName"` // external reviewers via share link
	ParentID    string     `gorm:"size:36;index" json:"parentId"`
	Resolved    bool       `json:"resolved"`
	Mentions    StringList `json:"mentions"`
	ShareLinkID string     `gorm:"size:36" json:"shareLinkId"`
	IssueID     string     `gorm:"size:36" json:"issueId"`
}

const (
	IssueOpen       = "open"
	IssueInProgress = "in_progress"
	IssueVerifying  = "verifying"
	IssueClosed     = "closed"
	IssueDisputed   = "disputed"
)

type Issue struct {
	Base
	StudioID    string     `gorm:"size:36;index" json:"studioId"`
	BookID      string     `gorm:"size:36;index" json:"bookId"`
	ChapterID   string     `gorm:"size:36;index" json:"chapterId"`
	SegmentID   string     `gorm:"size:36;index" json:"segmentId"`
	AtMs        *int64     `json:"atMs"`
	Type        string     `gorm:"size:32" json:"type"`
	Source      string     `gorm:"size:16" json:"source"` // manual | auto_review | comment | self
	Title       string     `gorm:"size:200" json:"title"`
	Description string     `gorm:"type:text" json:"description"`
	Attachment  string     `gorm:"size:36" json:"attachmentAssetId"`
	Status      string     `gorm:"size:16;index" json:"status"`
	AssigneeID  string     `gorm:"size:36;index" json:"assigneeId"`
	ReviewerID  string     `gorm:"size:36" json:"reviewerId"`
	ReporterID  string     `gorm:"size:36" json:"reporterId"`
	OldTakeID   string     `gorm:"size:36" json:"oldTakeId"`
	NewTakeID   string     `gorm:"size:36" json:"newTakeId"`
	Detail      Map        `json:"detail"`
	ClosedAt    *time.Time `json:"closedAt"`
	Severity    string     `gorm:"size:16" json:"severity"`
	DisputeNote string     `gorm:"size:500" json:"disputeNote"`
}

type ShareLink struct {
	Base
	StudioID      string     `gorm:"size:36;index" json:"studioId"`
	BookID        string     `gorm:"size:36;index" json:"bookId"`
	ChapterIDs    StringList `json:"chapterIds"`
	Token         string     `gorm:"size:64;uniqueIndex" json:"token"`
	Mode          string     `gorm:"size:16" json:"mode"` // review | listen
	PasswordHash  string     `gorm:"size:255" json:"-"`
	HasPassword   bool       `json:"hasPassword"`
	ExpiresAt     *time.Time `json:"expiresAt"`
	AllowComment  bool       `json:"allowComment"`
	AllowDownload bool       `json:"allowDownload"`
	CreatedBy     string     `gorm:"size:36" json:"createdBy"`
	RevokedAt     *time.Time `json:"revokedAt"`
	Views         int        `json:"views"`
	Name          string     `gorm:"size:100" json:"name"`
}

type ActivityLog struct {
	Base
	StudioID   string `gorm:"size:36;index" json:"studioId"`
	BookID     string `gorm:"size:36;index" json:"bookId"`
	ActorID    string `gorm:"size:36" json:"actorId"`
	Action     string `gorm:"size:64" json:"action"`
	TargetType string `gorm:"size:32" json:"targetType"`
	TargetID   string `gorm:"size:36" json:"targetId"`
	Summary    string `gorm:"size:500" json:"summary"`
	Detail     Map    `json:"detail"`
}

type AuditLog struct {
	Base
	ActorID    string `gorm:"size:36;index" json:"actorId"`
	Action     string `gorm:"size:64;index" json:"action"`
	TargetType string `gorm:"size:32" json:"targetType"`
	TargetID   string `gorm:"size:36" json:"targetId"`
	Reason     string `gorm:"size:500" json:"reason"`
	IP         string `gorm:"size:64" json:"ip"`
	Detail     Map    `json:"detail"`
}

type Notification struct {
	Base
	UserID   string     `gorm:"size:36;index" json:"userId"`
	StudioID string     `gorm:"size:36" json:"studioId"`
	Type     string     `gorm:"size:32" json:"type"`
	Title    string     `gorm:"size:200" json:"title"`
	Body     string     `gorm:"size:1000" json:"body"`
	Link     string     `gorm:"size:500" json:"link"`
	ReadAt   *time.Time `gorm:"index" json:"readAt"`
	Data     Map        `json:"data"`
}

// ---------- Operations ----------

type Announcement struct {
	Base
	Title     string     `gorm:"size:200" json:"title"`
	Body      string     `gorm:"type:text" json:"body"`
	Kind      string     `gorm:"size:16" json:"kind"`  // banner | modal
	Level     string     `gorm:"size:16" json:"level"` // info | success | warning
	Audience  Map        `json:"audience"`             // {planCodes:[], studioIds:[]}
	StartsAt  *time.Time `json:"startsAt"`
	EndsAt    *time.Time `json:"endsAt"`
	Published bool       `json:"published"`
	Link      string     `gorm:"size:500" json:"link"`
}

type Article struct {
	Base
	Kind      string `gorm:"size:16;index" json:"kind"` // help | changelog | legal
	Slug      string `gorm:"size:32;index" json:"slug"` // stable id of legal documents (terms, privacy, copyright, ai)
	Category  string `gorm:"size:64" json:"category"`
	Title     string `gorm:"size:200" json:"title"`
	Summary   string `gorm:"size:500" json:"summary"`
	Body      string `gorm:"type:text" json:"body"`
	Version   string `gorm:"size:32" json:"version"`
	SortOrder int    `json:"sortOrder"`
	Published bool   `json:"published"`
	Featured  bool   `json:"featured"`
}

type Feedback struct {
	Base
	UserID  string `gorm:"size:36;index" json:"userId"`
	Kind    string `gorm:"size:16" json:"kind"` // suggestion | bug | other
	Content string `gorm:"type:text" json:"content"`
	Contact string `gorm:"size:128" json:"contact"`
	Page    string `gorm:"size:255" json:"page"`
	Status  string `gorm:"size:16" json:"status"` // open | replied | closed
	Reply   string `gorm:"type:text" json:"reply"`
}

type Report struct {
	Base
	ReporterID string `gorm:"size:36" json:"reporterId"`
	TargetType string `gorm:"size:16" json:"targetType"` // voice | book | user | asset
	TargetID   string `gorm:"size:36" json:"targetId"`
	Reason     string `gorm:"size:32" json:"reason"`
	Detail     string `gorm:"size:1000" json:"detail"`
	Status     string `gorm:"size:16;index" json:"status"` // open | resolved | rejected
	Resolution string `gorm:"size:500" json:"resolution"`
	HandlerID  string `gorm:"size:36" json:"handlerId"`
}

type ModerationRecord struct {
	Base
	StudioID   string `gorm:"size:36;index" json:"studioId"`
	BookID     string `gorm:"size:36;index" json:"bookId"`
	TargetType string `gorm:"size:16" json:"targetType"` // book | chapter | quick
	TargetID   string `gorm:"size:36" json:"targetId"`
	Result     string `gorm:"size:16;index" json:"result"` // pass | review | block
	Source     string `gorm:"size:16" json:"source"`       // words | llm
	Hits       Map    `json:"hits"`
	Status     string `gorm:"size:16;index" json:"status"` // pending | approved | rejected | auto
	ReviewerID string `gorm:"size:36" json:"reviewerId"`
	Note       string `gorm:"size:500" json:"note"`
	Excerpt    string `gorm:"size:1000" json:"excerpt"`
}

type SensitiveWord struct {
	Base
	Word     string `gorm:"size:64;uniqueIndex" json:"word"`
	Level    string `gorm:"size:16" json:"level"` // block | review
	Category string `gorm:"size:32" json:"category"`
}

type SystemSetting struct {
	Key       string    `gorm:"primaryKey;size:64" json:"key"`
	Value     string    `gorm:"type:text" json:"value"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// StoredObject records a storage key whose file has a copy in the remote object store (Aliyun OSS).
type StoredObject struct {
	Key       string    `gorm:"column:object_key;primaryKey;size:255" json:"key"`
	Backend   string    `gorm:"size:16" json:"backend"`
	Bucket    string    `gorm:"size:64" json:"bucket"`
	Size      int64     `json:"size"`
	CreatedAt time.Time `json:"createdAt"`
}

// ---------- Open platform ----------

type APIKey struct {
	Base
	StudioID    string     `gorm:"size:36;index" json:"studioId"`
	Name        string     `gorm:"size:64" json:"name"`
	Prefix      string     `gorm:"size:16" json:"prefix"`
	KeyHash     string     `gorm:"size:64;uniqueIndex" json:"-"`
	Scopes      StringList `json:"scopes"` // read | produce | tts
	IPAllowlist StringList `json:"ipAllowlist"`
	RateLimit   int        `json:"rateLimit"` // requests per minute
	LastUsedAt  *time.Time `json:"lastUsedAt"`
	Disabled    bool       `json:"disabled"`
	CreatedBy   string     `gorm:"size:36" json:"createdBy"`
	Calls       int64      `json:"calls"`
}

type Webhook struct {
	Base
	StudioID string     `gorm:"size:36;index" json:"studioId"`
	URL      string     `gorm:"size:500" json:"url"`
	Secret   string     `gorm:"size:64" json:"-"`
	Events   StringList `json:"events"`
	Enabled  bool       `json:"enabled"`
	Note     string     `gorm:"size:255" json:"note"`
}

type WebhookDelivery struct {
	Base
	WebhookID    string `gorm:"size:36;index" json:"webhookId"`
	StudioID     string `gorm:"size:36;index" json:"studioId"`
	Event        string `gorm:"size:64" json:"event"`
	Payload      string `gorm:"type:text" json:"payload"`
	Status       string `gorm:"size:16" json:"status"` // pending | success | failed
	Attempts     int    `json:"attempts"`
	ResponseCode int    `json:"responseCode"`
	Error        string `gorm:"size:500" json:"error"`
	DurationMs   int64  `json:"durationMs"`
}

// ---------- Distribution ----------

type Channel struct {
	Base
	StudioID       string `gorm:"size:36;index" json:"studioId"`
	Kind           string `gorm:"size:16" json:"kind"` // rss | webhook | ximalaya | qingting | lizhi | spotify | apple
	Name           string `gorm:"size:64" json:"name"`
	Config         Map    `json:"config"`
	CredentialsEnc string `gorm:"type:text" json:"-"`
	Token          string `gorm:"size:64;index" json:"token"` // public feed token
	Enabled        bool   `json:"enabled"`
}

type Publication struct {
	Base
	StudioID   string     `gorm:"size:36;index" json:"studioId"`
	BookID     string     `gorm:"size:36;index" json:"bookId"`
	ChannelID  string     `gorm:"size:36;index" json:"channelId"`
	Status     string     `gorm:"size:16" json:"status"` // scheduled | publishing | published | failed | paused
	StartAt    time.Time  `json:"startAt"`
	IntervalH  int        `json:"intervalHours"` // hours between batches, 0 = all at once
	PerBatch   int        `json:"perBatch"`
	ChapterIDs StringList `json:"chapterIds"`
	Error      string     `gorm:"size:500" json:"error"`
	CreatedBy  string     `gorm:"size:36" json:"createdBy"`
	Checks     Map        `json:"checks"`
}

type PublishedEpisode struct {
	Base
	PublicationID string     `gorm:"size:36;index" json:"publicationId"`
	BookID        string     `gorm:"size:36;index" json:"bookId"`
	ChapterID     string     `gorm:"size:36;index" json:"chapterId"`
	ChannelID     string     `gorm:"size:36;index" json:"channelId"`
	RenderID      string     `gorm:"size:36" json:"renderId"`
	Status        string     `gorm:"size:16;index" json:"status"` // scheduled | published | failed
	PublishAt     time.Time  `gorm:"index" json:"publishAt"`
	PublishedAt   *time.Time `json:"publishedAt"`
	ExternalURL   string     `gorm:"size:500" json:"externalUrl"`
	Error         string     `gorm:"size:500" json:"error"`
	Title         string     `gorm:"size:200" json:"title"`
	Index         int        `json:"index"`
}

// ---------- Pipelines & exports ----------

type PipelineConfig struct {
	Mode             string   `json:"mode"`
	Tier             string   `json:"tier"`
	NarratorVoiceID  string   `json:"narratorVoiceId"`
	BGM              bool     `json:"bgm"`
	BGMAssetID       string   `json:"bgmAssetId"`
	IntroTemplateID  string   `json:"introTemplateId"`
	OutroTemplateID  string   `json:"outroTemplateId"`
	Preset           string   `json:"preset"`
	PauseAfterCast   bool     `json:"pauseAfterCast"`
	ChapterIDs       []string `json:"chapterIds"`
	RemoveSpeechTags bool     `json:"removeSpeechTags"`
}

type PipelineRun struct {
	Base
	StudioID      string               `gorm:"size:36;index" json:"studioId"`
	BookID        string               `gorm:"size:36;index" json:"bookId"`
	Config        JSON[PipelineConfig] `json:"config"`
	Status        string               `gorm:"size:16;index" json:"status"` // queued | running | paused | waiting | completed | failed | canceled
	Stage         string               `gorm:"size:32" json:"stage"`
	Progress      float64              `json:"progress"`
	Stages        Map                  `json:"stages"` // stage -> {status, progress}
	ChapterStates Map                  `json:"chapterStates"`
	JobID         string               `gorm:"size:36" json:"jobId"`
	EstCredits    int64                `json:"estCredits"`
	UsedCredits   int64                `json:"usedCredits"`
	Priority      int                  `json:"priority"`
	Error         string               `gorm:"size:1000" json:"error"`
	CreatedBy     string               `gorm:"size:36" json:"createdBy"`
	StartedAt     *time.Time           `json:"startedAt"`
	FinishedAt    *time.Time           `json:"finishedAt"`
	APIKeyID      string               `gorm:"size:36" json:"apiKeyId"`
}

type Export struct {
	Base
	StudioID   string     `gorm:"size:36;index" json:"studioId"`
	BookID     string     `gorm:"size:36;index" json:"bookId"`
	ChapterIDs StringList `json:"chapterIds"`
	Format     string     `gorm:"size:16" json:"format"` // mp3 | wav | m4a | m4b
	Options    Map        `json:"options"`
	Status     string     `gorm:"size:16" json:"status"`
	AssetID    string     `gorm:"size:36" json:"assetId"`
	Size       int64      `json:"size"`
	JobID      string     `gorm:"size:36" json:"jobId"`
	Error      string     `gorm:"size:500" json:"error"`
	CreatedBy  string     `gorm:"size:36" json:"createdBy"`
	FileName   string     `gorm:"size:255" json:"fileName"`
}

// All lists every model for auto-migration.
func All() []any {
	return []any{
		&User{}, &Session{}, &OAuthIdentity{}, &VerificationCode{}, &Studio{}, &StudioMember{}, &Invitation{},
		&Book{}, &Series{}, &BookMember{}, &Chapter{}, &Character{}, &Script{}, &Segment{}, &Take{},
		&Mix{}, &Render{}, &LexiconEntry{}, &Recording{}, &ReviewRun{},
		&Voice{}, &VoiceConsent{}, &VoiceFavorite{}, &VoiceListing{}, &Withdrawal{},
		&Asset{}, &AssetFavorite{}, &AudioTemplate{}, &EffectPreset{}, &QuickTTS{}, &VoiceDesign{},
		&ProviderAccount{}, &RoutingRule{}, &PromptTemplate{}, &EvalSet{}, &Job{},
		&Plan{}, &CreditLot{}, &CreditLedger{}, &UsageRecord{}, &PricingRule{}, &PromoCode{}, &PromoRedemption{},
		&UserMembership{}, &MembershipPeriod{}, &RedeemBatch{}, &RedeemCode{}, &RedeemUse{},
		&WorkflowTemplate{}, &ProductionTask{}, &PayRate{}, &Comment{}, &Issue{}, &ShareLink{},
		&ActivityLog{}, &AuditLog{}, &Notification{}, &Announcement{}, &Article{}, &Feedback{},
		&Report{}, &ModerationRecord{}, &SensitiveWord{}, &SystemSetting{}, &StoredObject{},
		&APIKey{}, &Webhook{}, &WebhookDelivery{}, &Channel{}, &Publication{}, &PublishedEpisode{},
		&PipelineRun{}, &Export{},
	}
}
