package core

import (
	"slices"

	"voicebook/internal/model"
)

type Perm string

const (
	PermView     Perm = "view"
	PermComment  Perm = "comment"
	PermEditInfo Perm = "edit_info"
	PermManage   Perm = "manage" // crew, settings, workflow
	PermDelete   Perm = "delete"
	PermScript   Perm = "script"
	PermCasting  Perm = "casting"
	PermGenerate Perm = "generate"
	PermRecord   Perm = "record"
	PermAlign    Perm = "align"
	PermPost     Perm = "post"
	PermReview   Perm = "review"
	PermExport   Perm = "export"
)

var allPerms = []Perm{PermView, PermComment, PermEditInfo, PermManage, PermDelete, PermScript, PermCasting, PermGenerate, PermRecord, PermAlign, PermPost, PermReview, PermExport}

var positionPerms = map[string][]Perm{
	model.PosLead:   {PermView, PermComment, PermEditInfo, PermManage, PermScript, PermCasting, PermGenerate, PermRecord, PermAlign, PermPost, PermReview, PermExport},
	model.PosScript: {PermView, PermComment, PermEditInfo, PermScript, PermCasting, PermGenerate},
	model.PosVoice:  {PermView, PermComment, PermGenerate, PermRecord, PermAlign},
	model.PosPost:   {PermView, PermComment, PermGenerate, PermAlign, PermPost, PermExport},
	model.PosReview: {PermView, PermComment, PermReview},
}

// Access describes what a user may do within a studio and (optionally) a book.
type Access struct {
	User      *model.User
	StudioID  string
	Role      string
	Member    *model.StudioMember
	Book      *model.Book
	Positions []string
	perms     map[Perm]bool
	// VoiceOnly restricts generation/recording to the user's assigned characters.
	VoiceOnly bool
}

func (x *Access) Can(p Perm) bool { return x.perms[p] }

func (x *Access) PermList() []string {
	var out []string
	for _, p := range allPerms {
		if x.perms[p] {
			out = append(out, string(p))
		}
	}
	return out
}

func (x *Access) IsManager() bool { return x.Role == model.RoleOwner || x.Role == model.RoleAdmin }

// StudioAccess resolves a user's role in a studio.
func (a *App) StudioAccess(u *model.User, studioID string) (*Access, error) {
	var m model.StudioMember
	if err := a.DB.First(&m, "studio_id = ? AND user_id = ? AND status = ?", studioID, u.ID, "active").Error; err != nil {
		return nil, Forbidden("你不是该工作室的成员")
	}
	var st model.Studio
	if err := a.DB.First(&st, "id = ? AND deleted_at IS NULL", studioID).Error; err != nil {
		return nil, NotFound("工作室")
	}
	if st.Status == "frozen" {
		return nil, Forbidden("该工作室已被冻结，请联系平台客服")
	}
	return &Access{User: u, StudioID: studioID, Role: m.Role, Member: &m, perms: map[Perm]bool{}}, nil
}

func (x *Access) CanManageStudio() bool { return x.IsManager() }
func (x *Access) CanCreateBook() bool {
	return x.Role == model.RoleOwner || x.Role == model.RoleAdmin || x.Role == model.RoleProducer
}
func (x *Access) CanUploadAssets() bool { return x.Role != model.RoleGuest }
func (x *Access) CanBilling() bool      { return x.Role == model.RoleOwner }
func (x *Access) CanViewBilling() bool  { return x.IsManager() }

// BookAccess resolves permissions for a book.
func (a *App) BookAccess(u *model.User, bookID string) (*Access, error) {
	var b model.Book
	if err := a.DB.First(&b, "id = ? AND deleted_at IS NULL", bookID).Error; err != nil {
		return nil, NotFound("图书")
	}
	x, err := a.StudioAccess(u, b.StudioID)
	if err != nil {
		return nil, err
	}
	x.Book = &b
	var bm model.BookMember
	if a.DB.First(&bm, "book_id = ? AND user_id = ?", b.ID, u.ID).Error == nil {
		x.Positions = bm.Positions.V
	}
	grant := func(ps ...Perm) {
		for _, p := range ps {
			x.perms[p] = true
		}
	}
	switch {
	case x.IsManager():
		grant(allPerms...)
	case x.Role == model.RoleProducer && (b.CreatedBy == u.ID || b.LeadID == u.ID || slices.Contains(x.Positions, model.PosLead)):
		grant(allPerms...)
	default:
		if x.Role == model.RoleProducer {
			grant(PermView, PermComment)
		}
		if b.LeadID == u.ID {
			grant(positionPerms[model.PosLead]...)
		}
		for _, pos := range x.Positions {
			grant(positionPerms[pos]...)
		}
		if len(x.perms) == 0 {
			return nil, Forbidden("你没有被加入这本书的剧组")
		}
		// voice-only members may generate/record only for characters assigned to them
		onlyVoice := true
		for _, pos := range x.Positions {
			if pos != model.PosVoice && pos != model.PosReview {
				onlyVoice = false
			}
		}
		if b.LeadID == u.ID {
			onlyVoice = false
		}
		x.VoiceOnly = onlyVoice && slices.Contains(x.Positions, model.PosVoice)
	}
	return x, nil
}

// CanVoiceCharacter checks the character restriction for voice-only crew.
func (a *App) CanVoiceCharacter(x *Access, characterID string) bool {
	if !x.VoiceOnly {
		return true
	}
	var c model.Character
	if a.DB.Select("assignee_id").First(&c, "id = ?", characterID).Error != nil {
		return false
	}
	return c.AssigneeID == x.User.ID
}

// ChapterAccess loads a chapter and its book access.
func (a *App) ChapterAccess(u *model.User, chapterID string) (*Access, *model.Chapter, error) {
	var ch model.Chapter
	if err := a.DB.First(&ch, "id = ?", chapterID).Error; err != nil {
		return nil, nil, NotFound("章节")
	}
	x, err := a.BookAccess(u, ch.BookID)
	if err != nil {
		return nil, nil, err
	}
	return x, &ch, nil
}

// SegmentAccess loads a segment with access.
func (a *App) SegmentAccess(u *model.User, segmentID string) (*Access, *model.Segment, error) {
	var s model.Segment
	if err := a.DB.First(&s, "id = ?", segmentID).Error; err != nil {
		return nil, nil, NotFound("片段")
	}
	x, err := a.BookAccess(u, s.BookID)
	if err != nil {
		return nil, nil, err
	}
	return x, &s, nil
}

func need(x *Access, p Perm) error {
	if !x.Can(p) {
		return Forbidden("")
	}
	return nil
}

// Need is the exported permission guard used by handlers.
func Need(x *Access, p Perm) error { return need(x, p) }

// MemberIDs returns active member user ids of a studio.
func (a *App) MemberIDs(studioID string) []string {
	var ids []string
	a.DB.Model(&model.StudioMember{}).Where("studio_id = ? AND status = ?", studioID, "active").Pluck("user_id", &ids)
	return ids
}

// HasFeature reports whether a studio's plan (or admin override) includes a feature.
func (a *App) HasFeature(studioID, feature string) bool {
	var st model.Studio
	if a.DB.First(&st, "id = ?", studioID).Error != nil {
		return false
	}
	if slices.Contains(st.Features.V, feature) {
		return true
	}
	var p model.Plan
	if st.PlanID != "" && a.DB.First(&p, "id = ?", st.PlanID).Error == nil {
		return slices.Contains(p.Features.V, feature)
	}
	return false
}

func (a *App) RequireFeature(studioID, feature, label string) error {
	if a.HasFeature(studioID, feature) {
		return nil
	}
	return &Error{Status: 403, Code: "plan_required", Msg: "当前套餐未包含「" + label + "」，请升级套餐后使用"}
}
