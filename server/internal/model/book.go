package model

import "time"

const (
	BookPreparing = "preparing"
	BookProducing = "producing"
	BookCompleted = "completed"
	BookPublished = "published"
	BookArchived  = "archived"
	BookReviewing = "moderation" // blocked by content moderation (pre-review mode)

	ModeSingle = "single" // single narrator audiobook
	ModeMulti  = "multi"  // multi-cast audiobook
	ModeDrama  = "drama"  // radio drama

	TierEconomy  = "economy"
	TierStandard = "standard"
	TierPremium  = "premium"

	PosLead   = "lead"
	PosScript = "script"
	PosVoice  = "voice"
	PosPost   = "post"
	PosReview = "review"
)

type Copyright struct {
	Licensor  string `json:"licensor"`
	Type      string `json:"type"` // exclusive | non_exclusive | original | public_domain
	StartDate string `json:"startDate"`
	EndDate   string `json:"endDate"`
	FileAsset string `json:"fileAssetId"`
	Statement string `json:"statement"`
}

type BookSettings struct {
	QualityTier      string     `json:"qualityTier"`
	Pause            PauseRules `json:"pause"`
	LoudnessPreset   string     `json:"loudnessPreset"`
	IntroTemplateID  string     `json:"introTemplateId"`
	OutroTemplateID  string     `json:"outroTemplateId"`
	AILabel          bool       `json:"aiLabel"`
	SpeechRate       int        `json:"speechRate"`
	RemoveSpeechTags bool       `json:"removeSpeechTags"`
	MonologueReverb  bool       `json:"monologueReverb"`
	DuckingDb        float64    `json:"duckingDb"`
	AutoQC           bool       `json:"autoQc"`
	ProductionMode   bool       `json:"productionMode"`
	ExportFormat     string     `json:"exportFormat"`
	ExportBitrate    int        `json:"exportBitrate"`
	NamingRule       string     `json:"namingRule"`
}

type WorkflowStage struct {
	Key               string `json:"key"`
	Name              string `json:"name"`
	Kind              string `json:"kind"` // script | tts | record | align | post | review | custom
	NeedsApproval     bool   `json:"needsApproval"`
	DefaultAssigneeID string `json:"defaultAssigneeId"`
	DefaultDays       int    `json:"defaultDays"`
}

type Book struct {
	Base
	StudioID      string                `gorm:"size:36;index" json:"studioId"`
	Title         string                `gorm:"size:200" json:"title"`
	Author        string                `gorm:"size:100" json:"author"`
	Narrator      string                `gorm:"size:100" json:"narrator"`
	Publisher     string                `gorm:"size:100" json:"publisher"`
	Adaptation    string                `gorm:"size:16" json:"adaptation"` // original | adapted
	Description   string                `gorm:"type:text" json:"description"`
	CoverAssetID  string                `gorm:"size:36" json:"coverAssetId"`
	Language      string                `gorm:"size:16;default:zh-CN" json:"language"`
	Genre         string                `gorm:"size:32" json:"genre"`
	Tags          StringList            `json:"tags"`
	Mode          string                `gorm:"size:16" json:"mode"`
	Status        string                `gorm:"size:16;index" json:"status"`
	ISBN          string                `gorm:"size:32" json:"isbn"`
	SeriesID      string                `gorm:"size:36;index" json:"seriesId"`
	SeriesOrder   int                   `json:"seriesOrder"`
	Copyright     JSON[Copyright]       `json:"copyright"`
	Settings      JSON[BookSettings]    `json:"settings"`
	Workflow      JSON[[]WorkflowStage] `json:"workflow"`
	WorkflowOn    bool                  `json:"workflowOn"`
	WordCount     int                   `json:"wordCount"`
	Budget        int64                 `json:"budget"` // credit budget, 0 = unlimited
	CreatedBy     string                `gorm:"size:36" json:"createdBy"`
	LeadID        string                `gorm:"size:36" json:"leadId"`
	PipelineRunID string                `gorm:"size:36" json:"pipelineRunId"`
	SourceAssetID string                `gorm:"size:36" json:"sourceAssetId"`
	DeletedAt     *time.Time            `gorm:"index" json:"deletedAt"`
}

type Series struct {
	Base
	StudioID    string `gorm:"size:36;index" json:"studioId"`
	Name        string `gorm:"size:128" json:"name"`
	Description string `gorm:"size:1000" json:"description"`
	Author      string `gorm:"size:100" json:"author"`
	Language    string `gorm:"size:16" json:"language"`
}

type BookMember struct {
	Base
	BookID    string     `gorm:"size:36;uniqueIndex:idx_book_user" json:"bookId"`
	UserID    string     `gorm:"size:36;uniqueIndex:idx_book_user;index" json:"userId"`
	Positions StringList `json:"positions"`
	AddedBy   string     `gorm:"size:36" json:"addedBy"`
}

const (
	ChapterBody  = "body"
	ChapterFront = "front"
	ChapterBack  = "back"
)

type Chapter struct {
	Base
	BookID          string     `gorm:"size:36;index" json:"bookId"`
	Index           int        `gorm:"index" json:"index"`
	Title           string     `gorm:"size:200" json:"title"`
	Volume          string     `gorm:"size:100" json:"volume"`
	Kind            string     `gorm:"size:16;default:body" json:"kind"`
	Included        bool       `gorm:"default:true" json:"included"`
	SourceText      string     `gorm:"type:text" json:"-"`
	WordCount       int        `json:"wordCount"`
	CurrentScriptID string     `gorm:"size:36" json:"currentScriptId"`
	ScriptStale     bool       `json:"scriptStale"`
	RenderID        string     `gorm:"size:36" json:"renderId"`
	RenderStale     bool       `json:"renderStale"`
	DurationMs      int64      `json:"durationMs"`
	ReviewStatus    string     `gorm:"size:16" json:"reviewStatus"` // "" | passed | rejected
	EditLockUserID  string     `gorm:"size:36" json:"editLockUserId"`
	EditLockAt      *time.Time `json:"editLockAt"`
}

const (
	CharNarrator   = "narrator"
	CharLead       = "lead"
	CharSupporting = "supporting"
	CharMinor      = "minor"
	CharCrowd      = "crowd"
)

type VoiceParams struct {
	Speed   *float64 `json:"speed,omitempty"`
	Pitch   *float64 `json:"pitch,omitempty"`
	Volume  *float64 `json:"volume,omitempty"`
	Emotion string   `json:"emotion,omitempty"`
}

type Character struct {
	Base
	BookID       string                   `gorm:"size:36;index" json:"bookId"`
	Name         string                   `gorm:"size:64" json:"name"`
	Aliases      StringList               `json:"aliases"`
	Kind         string                   `gorm:"size:16" json:"kind"`
	Gender       string                   `gorm:"size:16" json:"gender"`   // male | female | unknown
	AgeGroup     string                   `gorm:"size:16" json:"ageGroup"` // child | teen | young | adult | middle | senior
	Identity     string                   `gorm:"size:200" json:"identity"`
	Personality  string                   `gorm:"size:500" json:"personality"`
	VoiceTraits  string                   `gorm:"size:500" json:"voiceTraits"`
	Profile      string                   `gorm:"type:text" json:"profile"`
	FirstChapter int                      `json:"firstChapter"`
	LineCount    int                      `json:"lineCount"`
	CharCount    int                      `json:"charCount"`
	VoiceID      string                   `gorm:"size:36;index" json:"voiceId"`
	Params       JSON[VoiceParams]        `json:"params"`
	Mode         string                   `gorm:"size:16;default:ai" json:"mode"` // ai | human
	AssigneeID   string                   `gorm:"size:36;index" json:"assigneeId"`
	ClaimStatus  string                   `gorm:"size:16" json:"claimStatus"` // "" | pending | confirmed
	ClaimantID   string                   `gorm:"size:36" json:"claimantId"`
	Color        string                   `gorm:"size:16" json:"color"`
	AvatarURL    string                   `gorm:"size:512" json:"avatarUrl"`
	Candidate    bool                     `json:"candidate"` // proposed by script parsing, awaiting confirmation
	UseCrowdPool bool                     `json:"useCrowdPool"`
	SortOrder    int                      `json:"sortOrder"`
	Casting      JSON[[]CastingCandidate] `json:"casting"`
}

type CastingCandidate struct {
	VoiceID string  `json:"voiceId"`
	Score   float64 `json:"score"`
	Reason  string  `json:"reason"`
}

type Script struct {
	Base
	ChapterID string `gorm:"size:36;index" json:"chapterId"`
	BookID    string `gorm:"size:36;index" json:"bookId"`
	Version   int    `json:"version"`
	Origin    string `gorm:"size:16" json:"origin"` // ai | manual | import | copy | sync
	Status    string `gorm:"size:16" json:"status"` // parsing | ready | failed
	JobID     string `gorm:"size:36" json:"jobId"`
	CreatedBy string `gorm:"size:36" json:"createdBy"`
	Note      string `gorm:"size:255" json:"note"`
	Stats     Map    `json:"stats"`
}

const (
	SegNarration = "narration"
	SegDialogue  = "dialogue"
	SegMonologue = "monologue"
	SegSfx       = "sfx"
	SegPause     = "pause"

	AudioNone       = "none"
	AudioQueued     = "queued"
	AudioGenerating = "generating"
	AudioReady      = "ready"
	AudioFailed     = "failed"
	AudioStale      = "stale"

	RecTodo     = "todo"
	RecRecorded = "recorded"
	RecRework   = "rework"
	RecApproved = "approved"
)

// Cue is a performance tag placed before the rune at offset At (len(text) = after the last character).
type Cue struct {
	At  int    `json:"at"`
	Tag string `json:"tag"`
}

type Pronunciation struct {
	Offset    int    `json:"offset"` // rune offset in Text
	Char      string `json:"char"`
	Pinyin    string `json:"pinyin"`
	Homophone string `json:"homophone,omitempty"`
}

type Segment struct {
	Base
	ScriptID       string                `gorm:"size:36;index" json:"scriptId"`
	ChapterID      string                `gorm:"size:36;index" json:"chapterId"`
	BookID         string                `gorm:"size:36;index" json:"bookId"`
	Seq            float64               `gorm:"index" json:"seq"`
	Para           int                   `json:"para"`
	SceneBreak     bool                  `json:"sceneBreak"`
	Type           string                `gorm:"size:16" json:"type"`
	CharacterID    string                `gorm:"size:36;index" json:"characterId"`
	Text           string                `gorm:"type:text" json:"text"`
	Removed        bool                  `json:"removed"` // speech tags dropped in drama mode (kept for fidelity)
	Emotion        string                `gorm:"size:16" json:"emotion"`
	Intensity      int                   `json:"intensity"`
	Tags           StringList            `json:"tags"`
	Speed          *float64              `json:"speed"`
	Volume         *float64              `json:"volume"`
	Pitch          *float64              `json:"pitch"`
	PauseBeforeMs  *int                  `json:"pauseBeforeMs"`
	PauseAfterMs   *int                  `json:"pauseAfterMs"`
	Instruction    string                `gorm:"size:500" json:"instruction"`
	Prons          JSON[[]Pronunciation] `json:"prons"`
	Cues           JSON[[]Cue]           `json:"cues"` // positional performance tags; the text itself stays untouched
	Effects        StringList            `json:"effects"`
	SfxAssetID     string                `gorm:"size:36" json:"sfxAssetId"`
	Confidence     float64               `json:"confidence"`
	NeedsReview    bool                  `json:"needsReview"`
	Locked         bool                  `json:"locked"`
	SelectedTakeID string                `gorm:"size:36" json:"selectedTakeId"`
	AudioStatus    string                `gorm:"size:16;default:none;index" json:"audioStatus"`
	RecordStatus   string                `gorm:"size:16;default:todo" json:"recordStatus"`
	ContentHash    string                `gorm:"size:64" json:"contentHash"`
	FreeRegens     int                   `json:"freeRegens"`
	QC             string                `gorm:"size:16" json:"qc"` // "" | pass | suspect
	QCNote         string                `gorm:"size:500" json:"qcNote"`
	LastError      string                `gorm:"size:500" json:"lastError"`
	Note           string                `gorm:"size:500" json:"note"`
	Modified       bool                  `json:"modified"`
}

const (
	TakeTTS    = "tts"
	TakeRecord = "record"
	TakeUpload = "upload"
	TakeAlign  = "align"
)

type Take struct {
	Base
	SegmentID   string `gorm:"size:36;index" json:"segmentId"`
	ChapterID   string `gorm:"size:36;index" json:"chapterId"`
	Origin      string `gorm:"size:16" json:"origin"`
	VoiceID     string `gorm:"size:36" json:"voiceId"`
	Provider    string `gorm:"size:32" json:"provider"`
	Model       string `gorm:"size:64" json:"model"`
	CacheKey    string `gorm:"size:64;index" json:"cacheKey"`
	ContentHash string `gorm:"size:64" json:"contentHash"`
	AssetID     string `gorm:"size:36" json:"assetId"`
	DurationMs  int    `json:"durationMs"`
	Status      string `gorm:"size:16" json:"status"`
	Error       string `gorm:"size:500" json:"error"`
	CreatedBy   string `gorm:"size:36" json:"createdBy"`
	Params      Map    `json:"params"`
	QC          string `gorm:"size:16" json:"qc"`
	QCDetail    Map    `json:"qcDetail"`
	Credits     int64  `json:"credits"`
	Note        string `gorm:"size:255" json:"note"`
}

// Mix holds the user-arranged non-dialogue tracks for a chapter; dialogue is derived
// from the script at render time.
type Mix struct {
	Base
	ChapterID string        `gorm:"size:36;uniqueIndex" json:"chapterId"`
	Tracks    JSON[[]Track] `json:"tracks"`
	DuckingDb float64       `json:"duckingDb"`
	Intro     bool          `json:"intro"`
	Outro     bool          `json:"outro"`
	Version   int           `json:"version"`
	UpdatedBy string        `gorm:"size:36" json:"updatedBy"`
}

type Track struct {
	ID      string   `json:"id"`
	Kind    string   `json:"kind"` // bgm | sfx | ambience | custom
	Name    string   `json:"name"`
	Volume  float64  `json:"volume"` // dB
	Muted   bool     `json:"muted"`
	Duck    bool     `json:"duck"`
	Effects []string `json:"effects"`
	Clips   []Clip   `json:"clips"`
}

type Clip struct {
	ID            string   `json:"id"`
	AssetID       string   `json:"assetId"`
	Name          string   `json:"name"`
	AnchorSegment string   `json:"anchorSegmentId,omitempty"`
	StartMs       int64    `json:"startMs"`  // absolute or relative to anchor
	OffsetMs      int64    `json:"offsetMs"` // trim at clip start
	DurationMs    int64    `json:"durationMs"`
	Fill          bool     `json:"fill"` // loop to cover the whole chapter
	Loop          bool     `json:"loop"`
	Volume        float64  `json:"volume"` // dB
	FadeInMs      int64    `json:"fadeInMs"`
	FadeOutMs     int64    `json:"fadeOutMs"`
	Effects       []string `json:"effects"`
}

type Render struct {
	Base
	ChapterID  string                `gorm:"size:36;index" json:"chapterId"`
	BookID     string                `gorm:"size:36;index" json:"bookId"`
	Status     string                `gorm:"size:16" json:"status"` // rendering | ready | failed
	AssetID    string                `gorm:"size:36" json:"assetId"`
	DurationMs int64                 `json:"durationMs"`
	Preset     string                `gorm:"size:32" json:"preset"`
	Format     string                `gorm:"size:8" json:"format"`
	Report     Map                   `json:"report"`
	Timings    JSON[[]SegmentTiming] `json:"-"`
	CreatedBy  string                `gorm:"size:36" json:"createdBy"`
	JobID      string                `gorm:"size:36" json:"jobId"`
	Error      string                `gorm:"size:500" json:"error"`
}

type SegmentTiming struct {
	SegmentID   string `json:"segmentId"`
	CharacterID string `json:"characterId"`
	StartMs     int64  `json:"startMs"`
	EndMs       int64  `json:"endMs"`
	Text        string `json:"text"`
}

type LexiconEntry struct {
	Base
	Scope       string `gorm:"size:16;index" json:"scope"` // platform | studio | book
	StudioID    string `gorm:"size:36;index" json:"studioId"`
	BookID      string `gorm:"size:36;index" json:"bookId"`
	Term        string `gorm:"size:64;index" json:"term"`
	Pinyin      string `gorm:"size:128" json:"pinyin"`
	Replacement string `gorm:"size:128" json:"replacement"`
	Note        string `gorm:"size:255" json:"note"`
	Status      string `gorm:"size:16;default:active" json:"status"` // active | candidate | submitted | rejected
	CreatedBy   string `gorm:"size:36" json:"createdBy"`
	Hits        int    `json:"hits"`
	SourceID    string `gorm:"size:36" json:"sourceId"` // submitted copy -> origin entry
}

type Recording struct {
	Base
	ChapterID   string            `gorm:"size:36;index" json:"chapterId"`
	BookID      string            `gorm:"size:36;index" json:"bookId"`
	UploaderID  string            `gorm:"size:36" json:"uploaderId"`
	AssetIDs    StringList        `json:"assetIds"`
	Mode        string            `gorm:"size:16" json:"mode"` // full | character
	CharacterID string            `gorm:"size:36" json:"characterId"`
	Status      string            `gorm:"size:16" json:"status"` // uploaded | processing | done | failed
	JobID       string            `gorm:"size:36" json:"jobId"`
	Error       string            `gorm:"size:500" json:"error"`
	Result      JSON[AlignResult] `json:"result"`
	DurationMs  int64             `json:"durationMs"`
	MergedAsset string            `gorm:"size:36" json:"mergedAssetId"`
}

type AlignResult struct {
	Matches []AlignMatch `json:"matches"`
	Extras  []AlignSpan  `json:"extras"`
	Missing []string     `json:"missing"`
	Stats   Map          `json:"stats"`
}

type AlignMatch struct {
	SegmentID    string      `json:"segmentId"`
	StartMs      int64       `json:"startMs"`
	EndMs        int64       `json:"endMs"`
	Confidence   float64     `json:"confidence"`
	TakeID       string      `json:"takeId"`
	Alternatives []AlignSpan `json:"alternatives"`
}

type AlignSpan struct {
	StartMs    int64   `json:"startMs"`
	EndMs      int64   `json:"endMs"`
	Text       string  `json:"text"`
	Similarity float64 `json:"similarity,omitempty"`
}

type ReviewRun struct {
	Base
	ChapterID string                `gorm:"size:36;index" json:"chapterId"`
	BookID    string                `gorm:"size:36" json:"bookId"`
	Status    string                `gorm:"size:16" json:"status"`
	JobID     string                `gorm:"size:36" json:"jobId"`
	Summary   Map                   `json:"summary"`
	Findings  JSON[[]ReviewFinding] `json:"findings"`
	CreatedBy string                `gorm:"size:36" json:"createdBy"`
	UsedASR   bool                  `json:"usedAsr"`
}

type ReviewFinding struct {
	ID        string `json:"id"`
	SegmentID string `json:"segmentId"`
	Type      string `json:"type"`     // missing | extra | wrong | pause | volume | clipping | noise | silence | speed
	Severity  string `json:"severity"` // low | medium | high
	Message   string `json:"message"`
	Start     int    `json:"start"` // rune offset in text for highlight
	End       int    `json:"end"`
	Expected  string `json:"expected"`
	Actual    string `json:"actual"`
	AtMs      int64  `json:"atMs"`
	Ignored   bool   `json:"ignored"`
	IssueID   string `json:"issueId"`
}
