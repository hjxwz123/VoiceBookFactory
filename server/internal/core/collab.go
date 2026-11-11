package core

import (
	"fmt"
	"strings"
	"time"

	"voicebook/internal/model"
	"voicebook/internal/secure"
)

// ---------- comments ----------

type CommentInput struct {
	ChapterID string   `json:"chapterId"`
	SegmentID string   `json:"segmentId"`
	AtMs      *int64   `json:"atMs"`
	Body      string   `json:"body"`
	ParentID  string   `json:"parentId"`
	Mentions  []string `json:"mentions"`
}

func (a *App) CreateComment(x *Access, in CommentInput) (*model.Comment, error) {
	if err := need(x, PermComment); err != nil {
		return nil, err
	}
	body := strings.TrimSpace(in.Body)
	if body == "" {
		return nil, Bad("评论内容不能为空")
	}
	c := &model.Comment{StudioID: x.StudioID, BookID: x.Book.ID, ChapterID: in.ChapterID, SegmentID: in.SegmentID, AtMs: in.AtMs, Body: body, AuthorID: x.User.ID, AuthorName: x.User.Nickname, ParentID: in.ParentID, Mentions: model.Strings(in.Mentions...)}
	if err := a.DB.Create(c).Error; err != nil {
		return nil, err
	}
	link := fmt.Sprintf("/books/%s/chapters/%s", x.Book.ID, in.ChapterID)
	if in.SegmentID != "" {
		link += "?segment=" + in.SegmentID
	}
	targets := append([]string{}, in.Mentions...)
	if in.ParentID != "" {
		var parent model.Comment
		if a.DB.First(&parent, "id = ?", in.ParentID).Error == nil && parent.AuthorID != x.User.ID {
			targets = append(targets, parent.AuthorID)
		}
	}
	var filtered []string
	for _, t := range targets {
		if t != x.User.ID {
			filtered = append(filtered, t)
		}
	}
	a.NotifyMany(filtered, x.StudioID, "comment", x.User.Nickname+" 在《"+x.Book.Title+"》中提到了你", clip(body, 80), link)
	a.emit(x.StudioID, eventChapter(x.Book.ID, in.ChapterID, "comments"))
	return c, nil
}

// ---------- issues ----------

var IssueTypes = map[string]string{
	"missing": "漏读", "wrong": "错读", "extra": "多读", "pronunciation": "读音", "performance": "情绪/演绎", "noise": "噪音/音质",
	"edit": "剪辑", "script": "画本错误", "other": "其他",
}

type IssueInput struct {
	ChapterID   string `json:"chapterId"`
	SegmentID   string `json:"segmentId"`
	AtMs        *int64 `json:"atMs"`
	Type        string `json:"type"`
	Title       string `json:"title"`
	Description string `json:"description"`
	AssigneeID  string `json:"assigneeId"`
	Attachment  string `json:"attachmentAssetId"`
	Severity    string `json:"severity"`
	CommentID   string `json:"commentId"`
}

func (a *App) CreateIssue(x *Access, in IssueInput) (*model.Issue, error) {
	if !x.Can(PermReview) && !x.Can(PermManage) && !x.Can(PermComment) {
		return nil, Forbidden("")
	}
	if _, ok := IssueTypes[in.Type]; !ok {
		in.Type = "other"
	}
	source := "manual"
	if in.CommentID != "" {
		source = "comment"
	}
	if !x.Can(PermReview) && !x.Can(PermManage) {
		source = "self"
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		title = IssueTypes[in.Type]
	}
	assignee := in.AssigneeID
	var s model.Segment
	if in.SegmentID != "" && a.DB.First(&s, "id = ?", in.SegmentID).Error == nil && assignee == "" {
		var c model.Character
		if a.DB.First(&c, "id = ?", s.CharacterID).Error == nil && c.Mode == "human" && c.AssigneeID != "" {
			assignee = c.AssigneeID
		} else if s.SelectedTakeID != "" {
			var t model.Take
			if a.DB.First(&t, "id = ?", s.SelectedTakeID).Error == nil {
				assignee = t.CreatedBy
			}
		}
		if in.ChapterID == "" {
			in.ChapterID = s.ChapterID
		}
	}
	iss := &model.Issue{StudioID: x.StudioID, BookID: x.Book.ID, ChapterID: in.ChapterID, SegmentID: in.SegmentID, AtMs: in.AtMs, Type: in.Type, Source: source, Title: truncateStr(title, 190),
		Description: in.Description, Attachment: in.Attachment, Status: model.IssueOpen, AssigneeID: assignee, ReporterID: x.User.ID, ReviewerID: x.User.ID, OldTakeID: s.SelectedTakeID,
		Severity: firstNonEmpty(in.Severity, "medium"), Detail: model.EmptyMap()}
	if err := a.DB.Create(iss).Error; err != nil {
		return nil, err
	}
	if in.CommentID != "" {
		a.DB.Model(&model.Comment{}).Where("id = ?", in.CommentID).Update("issue_id", iss.ID)
	}
	if assignee != "" && assignee != x.User.ID {
		a.Notify(assignee, x.StudioID, "issue", "新的返修工单："+iss.Title, clip(s.Text, 60), fmt.Sprintf("/books/%s/chapters/%s?segment=%s", x.Book.ID, in.ChapterID, in.SegmentID))
	}
	a.emit(x.StudioID, eventBook(x.Book.ID, "issues"))
	if in.ChapterID != "" {
		a.emit(x.StudioID, eventChapter(x.Book.ID, in.ChapterID, "issues"))
	}
	return iss, nil
}

func (a *App) ActOnIssue(x *Access, iss *model.Issue, action, note, assignee string, kind ...string) error {
	isReviewer := x.Can(PermReview) || x.Can(PermManage) || iss.ReviewerID == x.User.ID
	isAssignee := iss.AssigneeID == x.User.ID || x.Can(PermManage)
	set := map[string]any{}
	notifyTo, msg := "", ""
	switch action {
	case "start":
		if !isAssignee {
			return Forbidden("只有处理人可以开始处理")
		}
		set["status"] = model.IssueInProgress
	case "submit":
		if !isAssignee {
			return Forbidden("只有处理人可以提交复核")
		}
		set["status"] = model.IssueVerifying
		notifyTo, msg = iss.ReviewerID, "工单已处理，等待你复核："+iss.Title
	case "close":
		if !isReviewer {
			return Forbidden("只有审听人可以关闭工单")
		}
		set["status"], set["closed_at"] = model.IssueClosed, ptrTime(now())
		notifyTo, msg = iss.AssigneeID, "工单已关闭："+iss.Title
	case "reopen":
		if !isReviewer {
			return Forbidden("只有审听人可以退回工单")
		}
		set["status"], set["closed_at"] = model.IssueOpen, nil
		notifyTo, msg = iss.AssigneeID, "工单被退回，请重新处理："+iss.Title
	case "dispute":
		if !isAssignee {
			return Forbidden("")
		}
		set["status"], set["dispute_note"] = model.IssueDisputed, note
		notifyTo, msg = iss.ReviewerID, "处理人对工单有异议："+iss.Title
	case "note": // add details (and the kind of problem) to a mark, e.g. after a quick mark while listening
		if !isReviewer && iss.ReporterID != x.User.ID && !isAssignee {
			return Forbidden("")
		}
		if len(kind) > 0 && kind[0] != "" {
			if name, ok := IssueTypes[kind[0]]; ok {
				set["type"] = kind[0]
				if iss.Title == IssueTypes[iss.Type] || iss.Title == "需要返工" {
					set["title"] = name
				}
			}
		}
		if strings.TrimSpace(note) == "" && len(set) == 0 {
			return nil
		}
		notifyTo, msg = iss.AssigneeID, "工单补充了说明："+iss.Title
	case "withdraw": // the reporter takes back a mark made by mistake (e.g. a quick mark while proof-listening)
		if iss.ReporterID != x.User.ID || iss.Status != model.IssueOpen {
			return Forbidden("只能撤回自己刚提的、还没开始处理的工单")
		}
		if err := a.DB.Delete(iss).Error; err != nil {
			return err
		}
		a.emit(x.StudioID, eventBook(iss.BookID, "issues"))
		if iss.ChapterID != "" {
			a.emit(x.StudioID, eventChapter(iss.BookID, iss.ChapterID, "issues"))
		}
		return nil
	case "assign":
		if !isReviewer {
			return Forbidden("")
		}
		set["assignee_id"] = assignee
		notifyTo, msg = assignee, "你被指派处理工单："+iss.Title
	default:
		return Bad("未知操作")
	}
	if note != "" && action != "dispute" {
		set["description"] = strings.TrimSpace(iss.Description + "\n\n" + x.User.Nickname + "：" + note)
	}
	if err := a.DB.Model(iss).Updates(set).Error; err != nil {
		return err
	}
	if notifyTo != "" && notifyTo != x.User.ID {
		a.Notify(notifyTo, x.StudioID, "issue", msg, note, fmt.Sprintf("/books/%s/chapters/%s?segment=%s", iss.BookID, iss.ChapterID, iss.SegmentID))
	}
	a.emit(x.StudioID, eventBook(iss.BookID, "issues"))
	if iss.ChapterID != "" {
		a.emit(x.StudioID, eventChapter(iss.BookID, iss.ChapterID, "issues"))
	}
	return nil
}

// ---------- share links ----------

type ShareInput struct {
	ChapterIDs    []string `json:"chapterIds"`
	Mode          string   `json:"mode"`
	Password      string   `json:"password"`
	ExpiresDays   int      `json:"expiresDays"`
	AllowComment  bool     `json:"allowComment"`
	AllowDownload bool     `json:"allowDownload"`
	Name          string   `json:"name"`
}

func (a *App) CreateShareLink(x *Access, in ShareInput) (*model.ShareLink, error) {
	if !x.Can(PermExport) && !x.Can(PermReview) && !x.Can(PermManage) {
		return nil, Forbidden("")
	}
	if in.Mode != "listen" {
		in.Mode = "review"
	}
	l := &model.ShareLink{StudioID: x.StudioID, BookID: x.Book.ID, ChapterIDs: model.Strings(in.ChapterIDs...), Token: secure.Token(18), Mode: in.Mode, AllowComment: in.AllowComment && in.Mode == "review", AllowDownload: in.AllowDownload, CreatedBy: x.User.ID, Name: firstNonEmpty(in.Name, x.Book.Title)}
	if in.Password != "" {
		h, _ := secure.HashPassword(in.Password)
		l.PasswordHash, l.HasPassword = h, true
	}
	if in.ExpiresDays > 0 {
		exp := now().AddDate(0, 0, in.ExpiresDays)
		l.ExpiresAt = &exp
	}
	return l, a.DB.Create(l).Error
}

func (a *App) ShareLinkByToken(token string) (*model.ShareLink, error) {
	var l model.ShareLink
	if a.DB.First(&l, "token = ?", token).Error != nil || l.RevokedAt != nil {
		return nil, NotFound("分享链接")
	}
	if l.ExpiresAt != nil && l.ExpiresAt.Before(now()) {
		return nil, Bad("分享链接已过期")
	}
	return &l, nil
}

// ShareView returns the public payload of a share link after password verification.
func (a *App) ShareView(l *model.ShareLink, password string) (map[string]any, error) {
	if l.HasPassword && !secure.CheckPassword(l.PasswordHash, password) {
		return nil, &Error{Status: 401, Code: "password_required", Msg: "请输入访问密码"}
	}
	var b model.Book
	if a.DB.First(&b, "id = ?", l.BookID).Error != nil {
		return nil, NotFound("作品")
	}
	a.DB.Model(l).Update("views", l.Views+1)
	q := a.DB.Where("book_id = ? AND included = ? AND render_id <> ''", b.ID, true).Order("\"index\" asc")
	if len(l.ChapterIDs.V) > 0 {
		q = q.Where("id IN ?", l.ChapterIDs.V)
	}
	var chs []model.Chapter
	q.Find(&chs)
	chars := map[string]map[string]string{}
	var cs []model.Character
	a.DB.Where("book_id = ?", b.ID).Find(&cs)
	for _, c := range cs {
		chars[c.ID] = map[string]string{"name": c.Name, "color": c.Color}
	}
	var items []map[string]any
	for _, ch := range chs {
		var r model.Render
		if a.DB.First(&r, "id = ?", ch.RenderID).Error != nil {
			continue
		}
		var as model.Asset
		a.DB.First(&as, "id = ?", r.AssetID)
		item := map[string]any{"id": ch.ID, "title": ch.Title, "index": ch.Index, "durationMs": r.DurationMs, "url": a.Store.SignedPath(as.StorageKey, 6*time.Hour, ""), "timings": r.Timings.V}
		if l.AllowDownload {
			item["downloadUrl"] = a.Store.SignedPath(as.StorageKey, 6*time.Hour, fmt.Sprintf("%02d_%s.%s", ch.Index, ch.Title, r.Format))
		}
		items = append(items, item)
	}
	var comments []model.Comment
	if l.AllowComment {
		a.DB.Where("share_link_id = ?", l.ID).Order("created_at asc").Find(&comments)
	}
	return map[string]any{
		"name": l.Name, "mode": l.Mode, "allowComment": l.AllowComment, "allowDownload": l.AllowDownload,
		"book":     map[string]any{"title": b.Title, "author": b.Author, "description": b.Description, "cover": a.AssetURLByID(b.CoverAssetID), "narrator": b.Narrator},
		"chapters": items, "characters": chars, "comments": comments, "site": a.Site().Name,
	}, nil
}

func (a *App) ShareComment(l *model.ShareLink, password, chapterID, segmentID string, atMs *int64, author, body string) (*model.Comment, error) {
	if !l.AllowComment {
		return nil, Forbidden("该链接不允许评论")
	}
	if l.HasPassword && !secure.CheckPassword(l.PasswordHash, password) {
		return nil, &Error{Status: 401, Code: "password_required", Msg: "请输入访问密码"}
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return nil, Bad("请输入评论内容")
	}
	if !a.Allow("share-comment:"+l.ID, 30) {
		return nil, TooMany("评论太频繁，请稍后再试")
	}
	c := &model.Comment{StudioID: l.StudioID, BookID: l.BookID, ChapterID: chapterID, SegmentID: segmentID, AtMs: atMs, Body: truncateStr(body, 2000), AuthorName: firstNonEmpty(truncateStr(author, 30), "访客"), ShareLinkID: l.ID, Mentions: model.Strings()}
	if err := a.DB.Create(c).Error; err != nil {
		return nil, err
	}
	a.Notify(l.CreatedBy, l.StudioID, "share_comment", c.AuthorName+" 通过分享链接留言", clip(body, 80), fmt.Sprintf("/books/%s/chapters/%s", l.BookID, chapterID))
	return c, nil
}
