package core

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"

	"voicebook/internal/docimport"
	"voicebook/internal/model"
	"voicebook/internal/text"
)

var charColors = []string{"#6366f1", "#ec4899", "#0ea5e9", "#f59e0b", "#10b981", "#8b5cf6", "#ef4444", "#14b8a6", "#f97316", "#84cc16", "#06b6d4", "#d946ef", "#eab308", "#22c55e", "#3b82f6", "#e11d48"}

// ---------- import preview ----------

type ImportChapter struct {
	Title     string `json:"title"`
	Volume    string `json:"volume"`
	Kind      string `json:"kind"`
	Included  bool   `json:"included"`
	WordCount int    `json:"wordCount"`
	Preview   string `json:"preview"`
	Text      string `json:"-"`
}

type ImportPreview struct {
	ID         string          `json:"id"`
	FileName   string          `json:"fileName"`
	Title      string          `json:"title"`
	Author     string          `json:"author"`
	Detection  string          `json:"detection"` // structure | rules | ai | size | none
	TotalWords int             `json:"totalWords"`
	Chapters   []ImportChapter `json:"chapters"`
	HasCover   bool            `json:"hasCover"`
	CoverExt   string          `json:"-"`
	FullText   string          `json:"-"`
}

type importFile struct {
	Preview  ImportPreview
	Texts    []string
	FullText string
}

func previewText(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	return truncateStr(s, 140)
}

func (a *App) saveImport(p *ImportPreview, cover []byte) error {
	f := importFile{Preview: *p, FullText: p.FullText}
	for _, c := range p.Chapters {
		f.Texts = append(f.Texts, c.Text)
	}
	b, _ := json.Marshal(f)
	if _, err := a.Store.PutBytes("tmp/imports/"+p.ID+".json", b); err != nil {
		return err
	}
	if len(cover) > 0 {
		a.Store.PutBytes("tmp/imports/"+p.ID+".cover."+p.CoverExt, cover)
	}
	return nil
}

func (a *App) loadImport(id string) (*ImportPreview, error) {
	if strings.ContainsAny(id, "/\\.") {
		return nil, Bad("无效的导入编号")
	}
	b, err := a.Store.ReadAll("tmp/imports/" + id + ".json")
	if err != nil {
		return nil, Bad("导入内容已过期，请重新上传")
	}
	var f importFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, err
	}
	p := f.Preview
	for i := range p.Chapters {
		if i < len(f.Texts) {
			p.Chapters[i].Text = f.Texts[i]
		}
	}
	p.FullText = f.FullText
	return &p, nil
}

func draftsToChapters(ds []text.ChapterDraft) []ImportChapter {
	var out []ImportChapter
	for _, d := range ds {
		out = append(out, ImportChapter{Title: d.Title, Volume: d.Volume, Kind: d.Kind, Included: d.Included, WordCount: d.WordCount, Preview: previewText(d.Text), Text: d.Text})
	}
	return out
}

var authorLine = regexp.MustCompile(`^\s*(?:作者|著|文)\s*[:：/]\s*(.{1,20})$`)

// GuessTitle reads a book title (and author) from the first lines of pasted text, e.g. "《长夜》" or
// "长夜\n作者：某某". It returns empty strings when the text starts straight with a chapter or prose.
func GuessTitle(raw string) (title, author string) {
	var lines []string
	for _, l := range strings.Split(raw, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
		if len(lines) == 3 {
			break
		}
	}
	if len(lines) < 2 {
		return "", ""
	}
	first := strings.TrimSpace(strings.Trim(lines[0], "《》<>【】[]"))
	n := utf8.RuneCountInString(first)
	if n == 0 || n > 24 || text.IsTitleLine(lines[0]) || text.IsVolumeLine(lines[0]) || strings.ContainsAny(first, "。！？!?，,；;：:“”\"") {
		return "", ""
	}
	for _, l := range lines[1:] {
		if m := authorLine.FindStringSubmatch(l); m != nil {
			author = strings.TrimSpace(m[1])
			break
		}
	}
	return first, author
}

// PreviewImport parses an uploaded manuscript (or pasted text) and detects chapters.
func (a *App) PreviewImport(fileName string, data []byte) (*ImportPreview, error) {
	site := a.Site()
	doc, err := docimport.Parse(fileName, data)
	if err != nil {
		return nil, Bad("%v", err)
	}
	full := text.Clean(doc.Text)
	words := text.WordCount(full)
	if words == 0 {
		return nil, Bad("没有读取到文字内容")
	}
	if site.MaxBookChars > 0 && words > site.MaxBookChars {
		return nil, Bad("书稿字数（%d）超过上限 %d 字，请分卷上传", words, site.MaxBookChars)
	}
	p := &ImportPreview{ID: model.NewID(), FileName: fileName, Title: doc.Title, Author: doc.Author, TotalWords: words, FullText: full}
	if p.Title == "" {
		p.Title = strings.TrimSuffix(filepath.Base(fileName), filepath.Ext(fileName))
		if p.Title == "" || p.Title == "." {
			p.Title = "未命名作品"
		}
	}
	// 1) structural sections (epub spine / docx headings) when they look like chapters
	if len(doc.Sections) > 1 {
		var drafts []text.ChapterDraft
		for _, s := range doc.Sections {
			body := text.Clean(s.Text)
			if text.WordCount(body) < 10 {
				continue
			}
			title := s.Title
			if title == "" {
				title = fmt.Sprintf("第%d章", len(drafts)+1)
			}
			kind, inc := "body", true
			if !text.IsTitleLine(title) {
				if ch, ok := text.SplitChapters(title + "\n" + body); ok && len(ch) == 1 {
					kind, inc = ch[0].Kind, ch[0].Included
				}
			}
			drafts = append(drafts, text.ChapterDraft{Title: title, Kind: kind, Included: inc, Text: body, WordCount: text.WordCount(body)})
		}
		if len(drafts) > 1 {
			p.Chapters, p.Detection = draftsToChapters(drafts), "structure"
		}
	}
	// 2) heading rules
	if p.Chapters == nil {
		if ds, ok := text.SplitChapters(full); ok && len(ds) > 0 {
			p.Chapters, p.Detection = draftsToChapters(ds), "rules"
		}
	}
	// 3) nothing found: short texts become one chapter; long texts are split by size until the user asks AI
	if p.Chapters == nil {
		if words <= 6000 {
			p.Chapters = draftsToChapters([]text.ChapterDraft{{Title: "第一章", Kind: "body", Included: true, Text: full, WordCount: words}})
			p.Detection = "single"
		} else {
			p.Chapters, p.Detection = draftsToChapters(text.SplitBySize(full, 3000, "第%d集")), "size"
		}
	}
	if len(doc.Cover) > 0 {
		p.HasCover = true
		p.CoverExt = doc.CoverExt
		if p.CoverExt == "" {
			p.CoverExt = "jpg"
		}
	}
	return p, a.saveImport(p, doc.Cover)
}

// ResplitImport re-splits the whole manuscript by size.
func (a *App) ResplitImport(id string, target int, titleFmt string) (*ImportPreview, error) {
	p, err := a.loadImport(id)
	if err != nil {
		return nil, err
	}
	p.Chapters = draftsToChapters(text.SplitBySize(p.FullText, target, titleFmt))
	p.Detection = "size"
	return p, a.saveImport(p, nil)
}

// DetectImportWithAI asks the LLM which short lines are chapter headings.
func (a *App) DetectImportWithAI(ctx context.Context, x *Access, id string) (*ImportPreview, error) {
	p, err := a.loadImport(id)
	if err != nil {
		return nil, err
	}
	h, err := a.LLMFor(FeatScript, "", x.StudioID)
	if err != nil {
		return nil, err
	}
	lines, idx := text.CandidateLines(p.FullText, 1500)
	if len(lines) == 0 {
		return nil, Bad("没有找到可能的章节标题，建议按字数自动分集")
	}
	picked, err := a.DetectChapterLines(ctx, h, lines)
	if err != nil {
		return nil, err
	}
	all := strings.Split(text.Clean(p.FullText), "\n")
	var at []int
	for _, k := range picked {
		if k >= 0 && k < len(idx) {
			at = append(at, idx[k])
		}
	}
	if len(at) == 0 {
		return nil, Bad("AI 没有识别出章节标题，建议按字数自动分集")
	}
	ds := text.SplitAtLines(all, at)
	if len(ds) == 0 {
		return nil, Bad("AI 识别结果不可用，建议按字数自动分集")
	}
	a.ChargeCredits(Charge{StudioID: x.StudioID, UserID: x.User.ID, Feature: FeatScript, Quantity: float64(len(lines) * 12), Unit: "chars", AccountID: h.Account.ID, Provider: h.Account.Provider, Model: h.Client.Model(), BYOK: h.BYOK, Note: "智能拆章"})
	p.Chapters, p.Detection = draftsToChapters(ds), "ai"
	return p, a.saveImport(p, nil)
}

// ChapterRef selects (and possibly merges) chapters of an import preview.
type ChapterRef struct {
	Sources  []int  `json:"sources"`
	Title    string `json:"title"`
	Included bool   `json:"included"`
}

// draftsFromImport builds chapter drafts from a preview; nil refs = take every detected chapter.
func draftsFromImport(p *ImportPreview, refs []ChapterRef) []ImportChapter {
	if len(refs) == 0 {
		return p.Chapters
	}
	var drafts []ImportChapter
	for _, c := range refs {
		var parts []string
		for _, s := range c.Sources {
			if s >= 0 && s < len(p.Chapters) {
				parts = append(parts, p.Chapters[s].Text)
			}
		}
		if len(parts) == 0 {
			continue
		}
		body := strings.Join(parts, "\n")
		t := strings.TrimSpace(c.Title)
		if t == "" {
			t = p.Chapters[c.Sources[0]].Title
		}
		drafts = append(drafts, ImportChapter{Title: t, Kind: p.Chapters[c.Sources[0]].Kind, Included: c.Included, Text: body, WordCount: text.WordCount(body)})
	}
	return drafts
}

type CreateBookInput struct {
	ImportID    string       `json:"importId"`
	Title       string       `json:"title"`
	Author      string       `json:"author"`
	Description string       `json:"description"`
	Mode        string       `json:"mode"`
	Language    string       `json:"language"`
	Genre       string       `json:"genre"`
	Tags        []string     `json:"tags"`
	Tier        string       `json:"tier"`
	Chapters    []ChapterRef `json:"chapters"`
	AutoAnalyze bool         `json:"autoAnalyze"`
	SeriesID    string       `json:"seriesId"`
}

func (a *App) defaultBookSettings(studioID, tier string) model.BookSettings {
	var st model.Studio
	a.DB.First(&st, "id = ?", studioID)
	ss := st.Settings.V
	if tier == "" {
		tier = model.TierStandard
	}
	if ss.Pause.SameSpeakerMs == 0 {
		ss = model.DefaultStudioSettings()
	}
	return model.BookSettings{
		QualityTier: tier, Pause: ss.Pause, LoudnessPreset: ss.DefaultPreset, IntroTemplateID: ss.IntroTemplateID, OutroTemplateID: ss.OutroTemplateID,
		SpeechRate: ss.SpeechRate, MonologueReverb: ss.MonologueReverb, DuckingDb: ss.DuckingDb, AutoQC: ss.AutoQC, ProductionMode: ss.ProductionMode,
		ExportFormat: ss.DefaultFormat, ExportBitrate: ss.DefaultBitrate, NamingRule: "{index}_{title}",
	}
}

func (a *App) CreateBook(ctx context.Context, x *Access, in CreateBookInput) (*model.Book, error) {
	if !x.CanCreateBook() {
		return nil, Forbidden("只有所有者、管理员或制作人可以创建图书")
	}
	if err := a.checkBookLimit(x.StudioID); err != nil {
		return nil, err
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		return nil, Bad("请填写书名")
	}
	mode := in.Mode
	if mode != model.ModeSingle && mode != model.ModeDrama {
		mode = model.ModeMulti
	}
	b := &model.Book{
		StudioID: x.StudioID, Title: truncateStr(title, 190), Author: in.Author, Description: in.Description, Mode: mode,
		Language: firstNonEmpty(in.Language, "zh-CN"), Genre: in.Genre, Tags: model.Strings(in.Tags...), Status: model.BookPreparing,
		CreatedBy: x.User.ID, LeadID: x.User.ID, SeriesID: in.SeriesID, Workflow: model.JSON[[]model.WorkflowStage]{V: []model.WorkflowStage{}},
	}
	b.Settings.V = a.defaultBookSettings(x.StudioID, in.Tier)
	b.Settings.V.RemoveSpeechTags = mode == model.ModeDrama
	b.Copyright.V = model.Copyright{}
	var drafts []ImportChapter
	var preview *ImportPreview
	if in.ImportID != "" {
		p, err := a.loadImport(in.ImportID)
		if err != nil {
			return nil, err
		}
		preview = p
		drafts = draftsFromImport(p, in.Chapters)
		if err := a.checkBookWords(x.StudioID, includedWords(drafts)); err != nil {
			return nil, err
		}
	}
	err := a.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(b).Error; err != nil {
			return err
		}
		if err := tx.Create(&model.BookMember{BookID: b.ID, UserID: x.User.ID, Positions: model.Strings(model.PosLead), AddedBy: x.User.ID}).Error; err != nil {
			return err
		}
		// every book has a narrator
		narr := model.Character{BookID: b.ID, Name: "旁白", Kind: model.CharNarrator, Gender: "unknown", AgeGroup: "adult", Color: "#64748b", Mode: "ai", Aliases: model.Strings(), Profile: "全书叙述者，负责朗读非对白内容。"}
		narr.Params.V = model.VoiceParams{}
		narr.Casting.V = []model.CastingCandidate{}
		if err := tx.Create(&narr).Error; err != nil {
			return err
		}
		words := 0
		for i, d := range drafts {
			ch := model.Chapter{BookID: b.ID, Index: i + 1, Title: truncateStr(d.Title, 190), Volume: d.Volume, Kind: firstNonEmpty(d.Kind, "body"), Included: d.Included, SourceText: d.Text, WordCount: text.WordCount(d.Text)}
			if err := tx.Create(&ch).Error; err != nil {
				return err
			}
			if ch.Included {
				words += ch.WordCount
			}
		}
		return tx.Model(b).Update("word_count", words).Error
	})
	if err != nil {
		return nil, err
	}
	// cover from epub
	if preview != nil && preview.HasCover {
		if data, err := a.Store.ReadAll("tmp/imports/" + preview.ID + ".cover." + preview.CoverExt); err == nil {
			if as, err := a.SaveAssetBytes(ctx, AssetInput{StudioID: x.StudioID, Kind: model.AssetImage, Name: b.Title + " 封面", Ext: preview.CoverExt, Origin: "upload", CreatedBy: x.User.ID}, data); err == nil {
				a.DB.Model(b).Update("cover_asset_id", as.ID)
				b.CoverAssetID = as.ID
			}
		}
	}
	a.Activity(x.StudioID, b.ID, x.User.ID, "book.create", "book", b.ID, "创建了图书《"+b.Title+"》")
	go a.ModerateBook(context.Background(), b.ID)
	if in.AutoAnalyze && len(drafts) > 0 {
		a.EnqueueCharacterAnalysis(x, b.ID, nil)
	}
	return b, nil
}

// ImportIntoBook appends chapters from an uploaded/pasted manuscript to an existing (possibly empty) book.
func (a *App) ImportIntoBook(ctx context.Context, x *Access, importID string, refs []ChapterRef, autoAnalyze bool) (int, error) {
	if err := need(x, PermScript); err != nil {
		return 0, err
	}
	p, err := a.loadImport(importID)
	if err != nil {
		return 0, err
	}
	drafts := draftsFromImport(p, refs)
	if len(drafts) == 0 {
		return 0, Bad("没有可导入的章节")
	}
	var existing int
	a.DB.Model(&model.Chapter{}).Where("book_id = ? AND included = ?", x.Book.ID, true).Select("COALESCE(SUM(word_count),0)").Scan(&existing)
	if err := a.checkBookWords(x.StudioID, existing+includedWords(drafts)); err != nil {
		return 0, err
	}
	var maxIdx int
	a.DB.Model(&model.Chapter{}).Where("book_id = ?", x.Book.ID).Select(`COALESCE(MAX("index"),0)`).Scan(&maxIdx)
	err = a.DB.Transaction(func(tx *gorm.DB) error {
		for i, d := range drafts {
			ch := model.Chapter{BookID: x.Book.ID, Index: maxIdx + i + 1, Title: truncateStr(d.Title, 190), Volume: d.Volume, Kind: firstNonEmpty(d.Kind, "body"), Included: d.Included, SourceText: d.Text, WordCount: text.WordCount(d.Text)}
			if err := tx.Create(&ch).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	if x.Book.CoverAssetID == "" && p.HasCover {
		if data, err := a.Store.ReadAll("tmp/imports/" + p.ID + ".cover." + p.CoverExt); err == nil {
			if as, err := a.SaveAssetBytes(ctx, AssetInput{StudioID: x.StudioID, Kind: model.AssetImage, Name: x.Book.Title + " 封面", Ext: p.CoverExt, Origin: "upload", CreatedBy: x.User.ID}, data); err == nil {
				a.DB.Model(x.Book).Update("cover_asset_id", as.ID)
			}
		}
	}
	a.RecountBook(x.Book.ID)
	a.Activity(x.StudioID, x.Book.ID, x.User.ID, "chapter.import", "book", x.Book.ID, fmt.Sprintf("导入了 %d 章（%s）", len(drafts), p.FileName))
	go a.ModerateBook(context.Background(), x.Book.ID)
	if autoAnalyze {
		var others int64
		a.DB.Model(&model.Character{}).Where("book_id = ? AND kind <> ?", x.Book.ID, model.CharNarrator).Count(&others)
		if others == 0 {
			a.EnqueueCharacterAnalysis(x, x.Book.ID, nil)
		}
	}
	a.emit(x.StudioID, eventBook(x.Book.ID, "chapters"))
	return len(drafts), nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func (a *App) checkBookLimit(studioID string) error {
	p := a.StudioPlan(studioID)
	if p.MaxBooks <= 0 {
		return nil
	}
	var n int64
	a.DB.Model(&model.Book{}).Where("studio_id = ? AND deleted_at IS NULL", studioID).Count(&n)
	if int(n) >= p.MaxBooks {
		return planLimit("books", int64(p.MaxBooks), n, "「%s」每个工作室最多 %d 本图书（回收站里的也算），可彻底删除不用的书或升级会员", p.Name, p.MaxBooks)
	}
	return nil
}

func includedWords(ds []ImportChapter) int {
	n := 0
	for _, d := range ds {
		if d.Included {
			n += d.WordCount
		}
	}
	return n
}

// checkBookWords enforces the per-book word limit of the studio's plan (falls back to the site setting).
func (a *App) checkBookWords(studioID string, words int) error {
	p := a.StudioPlan(studioID)
	limit := p.MaxBookWords
	if limit <= 0 {
		limit = a.Site().MaxBookChars
	}
	if limit > 0 && words > limit {
		return planLimit("bookWords", int64(limit), int64(words), "书稿共 %d 字，超过「%s」单本 %d 字的上限，可分卷导入或升级会员", words, p.Name, limit)
	}
	return nil
}

// RecountBook refreshes cached word counts and character line counts.
func (a *App) RecountBook(bookID string) {
	var words int64
	a.DB.Model(&model.Chapter{}).Where("book_id = ? AND included = ?", bookID, true).Select("COALESCE(SUM(word_count),0)").Scan(&words)
	a.DB.Model(&model.Book{}).Where("id = ?", bookID).Update("word_count", words)
	type row struct {
		CharacterID string
		Lines       int
		Chars       int
	}
	var rows []row
	a.DB.Raw(`SELECT s.character_id AS character_id, COUNT(*) AS lines, COALESCE(SUM(LENGTH(s.text)),0) AS chars
		FROM segments s JOIN chapters c ON c.current_script_id = s.script_id
		WHERE s.book_id = ? AND s.removed = ? AND s.type <> ? GROUP BY s.character_id`, bookID, false, model.SegSfx).Scan(&rows)
	a.DB.Model(&model.Character{}).Where("book_id = ?", bookID).Updates(map[string]any{"line_count": 0})
	for _, r := range rows {
		if r.CharacterID != "" {
			a.DB.Model(&model.Character{}).Where("id = ?", r.CharacterID).Updates(map[string]any{"line_count": r.Lines, "char_count": r.Chars})
		}
	}
}

// ---------- chapters ----------

func (a *App) ReindexChapters(bookID string) {
	var chs []model.Chapter
	a.DB.Where("book_id = ?", bookID).Order("\"index\" asc, created_at asc").Find(&chs)
	for i, c := range chs {
		if c.Index != i+1 {
			a.DB.Model(&model.Chapter{}).Where("id = ?", c.ID).Update("index", i+1)
		}
	}
}

// UpdateChapterText saves new source text and marks the script for syncing.
func (a *App) UpdateChapterText(x *Access, ch *model.Chapter, newText string) (map[string]any, error) {
	if err := need(x, PermScript); err != nil {
		return nil, err
	}
	newText = text.Clean(newText)
	oldParas, _ := text.Paragraphs(ch.SourceText)
	newParas, _ := text.Paragraphs(newText)
	ops := text.DiffStrings(oldParas, newParas)
	changed := 0
	for _, o := range ops {
		if o.Kind != "equal" {
			changed++
		}
	}
	up := map[string]any{"source_text": newText, "word_count": text.WordCount(newText)}
	if ch.CurrentScriptID != "" && changed > 0 {
		up["script_stale"] = true
	}
	if err := a.DB.Model(ch).Updates(up).Error; err != nil {
		return nil, err
	}
	a.RecountBook(ch.BookID)
	if changed > 0 && ch.CurrentScriptID != "" {
		var ids []string
		var members []model.BookMember
		a.DB.Where("book_id = ?", ch.BookID).Find(&members)
		for _, m := range members {
			if contains(m.Positions.V, model.PosScript) || contains(m.Positions.V, model.PosLead) {
				ids = append(ids, m.UserID)
			}
		}
		a.NotifyMany(ids, x.StudioID, "script_stale", "《"+x.Book.Title+"》"+ch.Title+" 的原文已修改", "请在制作台中同步画本", fmt.Sprintf("/books/%s/chapters/%s", ch.BookID, ch.ID))
	}
	a.Activity(x.StudioID, ch.BookID, x.User.ID, "chapter.text", "chapter", ch.ID, fmt.Sprintf("修改了「%s」的原文（%d 处段落变化）", ch.Title, changed))
	return map[string]any{"changedParagraphs": changed, "ops": ops, "scriptStale": changed > 0 && ch.CurrentScriptID != ""}, nil
}

// ChapterState derives a simple pipeline status for a chapter (used when workflow is off).
type ChapterState struct {
	Stage        string  `json:"stage"` // script | voice | render | review | done
	Label        string  `json:"label"`
	Segments     int     `json:"segments"`
	Ready        int     `json:"ready"`
	Failed       int     `json:"failed"`
	Stale        int     `json:"stale"`
	Missing      int     `json:"missing"`
	AudioPercent float64 `json:"audioPercent"`
}

func (a *App) ChapterStates(bookID string) map[string]ChapterState {
	var chs []model.Chapter
	a.DB.Select("id", "current_script_id", "render_id", "render_stale", "review_status").Where("book_id = ?", bookID).Find(&chs)
	type row struct {
		ScriptID string
		Status   string
		N        int
	}
	var rows []row
	a.DB.Raw(`SELECT s.script_id AS script_id, s.audio_status AS status, COUNT(*) AS n FROM segments s
		JOIN chapters c ON c.current_script_id = s.script_id WHERE c.book_id = ? AND s.removed = ? AND s.type NOT IN ?
		GROUP BY s.script_id, s.audio_status`, bookID, false, []string{model.SegSfx, model.SegPause}).Scan(&rows)
	counts := map[string]map[string]int{}
	for _, r := range rows {
		if counts[r.ScriptID] == nil {
			counts[r.ScriptID] = map[string]int{}
		}
		counts[r.ScriptID][r.Status] += r.N
	}
	out := map[string]ChapterState{}
	for _, c := range chs {
		st := ChapterState{}
		if c.CurrentScriptID == "" {
			st.Stage, st.Label = "script", "待生成画本"
			out[c.ID] = st
			continue
		}
		cnt := counts[c.CurrentScriptID]
		for k, v := range cnt {
			st.Segments += v
			switch k {
			case model.AudioReady:
				st.Ready += v
			case model.AudioFailed:
				st.Failed += v
			case model.AudioStale:
				st.Stale += v
			}
		}
		st.Missing = st.Segments - st.Ready
		if st.Segments > 0 {
			st.AudioPercent = float64(st.Ready) / float64(st.Segments)
		}
		switch {
		case st.Segments == 0 || st.Ready < st.Segments:
			st.Stage, st.Label = "voice", "配音中"
			if st.Ready == 0 {
				st.Label = "待配音"
			}
		case c.RenderID == "" || c.RenderStale:
			st.Stage, st.Label = "render", "待合成"
		case c.ReviewStatus != "passed":
			st.Stage, st.Label = "review", "待审听"
		default:
			st.Stage, st.Label = "done", "已完成"
		}
		out[c.ID] = st
	}
	return out
}

// BookProgress summarizes the production pipeline for the guided stepper.
func (a *App) BookProgress(b *model.Book) map[string]any {
	var chapters, included, scripted, rendered, reviewed int64
	a.DB.Model(&model.Chapter{}).Where("book_id = ?", b.ID).Count(&chapters)
	a.DB.Model(&model.Chapter{}).Where("book_id = ? AND included = ?", b.ID, true).Count(&included)
	a.DB.Model(&model.Chapter{}).Where("book_id = ? AND included = ? AND current_script_id <> ''", b.ID, true).Count(&scripted)
	a.DB.Model(&model.Chapter{}).Where("book_id = ? AND included = ? AND render_id <> '' AND render_stale = ?", b.ID, true, false).Count(&rendered)
	a.DB.Model(&model.Chapter{}).Where("book_id = ? AND included = ? AND review_status = ?", b.ID, true, "passed").Count(&reviewed)
	var chars, cast, candidates int64
	a.DB.Model(&model.Character{}).Where("book_id = ? AND candidate = ?", b.ID, false).Count(&chars)
	a.DB.Model(&model.Character{}).Where("book_id = ? AND candidate = ? AND (voice_id <> '' OR (mode = 'human' AND assignee_id <> '') OR use_crowd_pool = ?)", b.ID, false, true).Count(&cast)
	a.DB.Model(&model.Character{}).Where("book_id = ? AND candidate = ?", b.ID, true).Count(&candidates)
	var segs, ready int64
	a.DB.Raw(`SELECT COUNT(*) FROM segments s JOIN chapters c ON c.current_script_id = s.script_id WHERE c.book_id = ? AND c.included = ? AND s.removed = ? AND s.type NOT IN ?`, b.ID, true, false, []string{model.SegSfx, model.SegPause}).Scan(&segs)
	a.DB.Raw(`SELECT COUNT(*) FROM segments s JOIN chapters c ON c.current_script_id = s.script_id WHERE c.book_id = ? AND c.included = ? AND s.removed = ? AND s.type NOT IN ? AND s.audio_status = ?`, b.ID, true, false, []string{model.SegSfx, model.SegPause}, model.AudioReady).Scan(&ready)
	var exports int64
	a.DB.Model(&model.Export{}).Where("book_id = ? AND status = ?", b.ID, "ready").Count(&exports)
	var analysisJobs int64
	a.DB.Model(&model.Job{}).Where("book_id = ? AND type = ? AND status = ?", b.ID, "character_analysis", model.JobSucceeded).Count(&analysisJobs)
	var duration int64
	a.DB.Model(&model.Chapter{}).Where("book_id = ? AND included = ?", b.ID, true).Select("COALESCE(SUM(duration_ms),0)").Scan(&duration)
	var credits int64
	a.DB.Model(&model.UsageRecord{}).Where("book_id = ?", b.ID).Select("COALESCE(SUM(credits),0)").Scan(&credits)
	rate := b.Settings.V.SpeechRate
	if rate <= 0 {
		rate = 220
	}
	step := func(done bool, partial float64) map[string]any {
		return map[string]any{"done": done, "progress": partial}
	}
	ratio := func(n, d int64) float64 {
		if d == 0 {
			return 0
		}
		return float64(n) / float64(d)
	}
	steps := map[string]any{
		"import":     step(chapters > 0, ratio(min(chapters, 1), 1)),
		"characters": step(analysisJobs > 0 || chars > 1, ratio(min(chars, 2), 2)),
		"casting":    step(chars > 0 && cast >= chars, ratio(cast, chars)),
		"script":     step(included > 0 && scripted >= included, ratio(scripted, included)),
		"voice":      step(segs > 0 && ready >= segs && scripted >= included, ratio(ready, segs)),
		"render":     step(included > 0 && rendered >= included, ratio(rendered, included)),
		"review":     step(included > 0 && reviewed >= included, ratio(reviewed, included)),
		"export":     step(exports > 0, ratio(min(exports, 1), 1)),
	}
	next := ""
	for _, k := range []string{"import", "characters", "casting", "script", "voice", "render", "review", "export"} {
		if !steps[k].(map[string]any)["done"].(bool) {
			next = k
			break
		}
	}
	return map[string]any{
		"chapters": chapters, "included": included, "scripted": scripted, "rendered": rendered, "reviewed": reviewed,
		"characters": chars, "cast": cast, "candidates": candidates, "segments": segs, "readySegments": ready,
		"durationMs": duration, "estimatedMs": int64(b.WordCount) * 60000 / int64(rate), "credits": credits,
		"steps": steps, "next": next,
	}
}

// ---------- AI helpers on books ----------

func (a *App) GenerateIntro(ctx context.Context, x *Access) (BookIntro, error) {
	var ch model.Chapter
	a.DB.Where("book_id = ? AND included = ?", x.Book.ID, true).Order("\"index\" asc").First(&ch)
	sample := truncateStr(ch.SourceText, 4000)
	if sample == "" {
		return BookIntro{}, Bad("图书还没有正文内容")
	}
	h, err := a.LLMFor(FeatScript, x.Book.Settings.V.QualityTier, x.StudioID)
	if err != nil {
		return BookIntro{}, err
	}
	intro, err := a.GenerateBookIntro(ctx, h, x.Book.Title, sample)
	if err == nil {
		a.ChargeCredits(Charge{StudioID: x.StudioID, UserID: x.User.ID, BookID: x.Book.ID, Feature: FeatScript, Quantity: float64(utf8.RuneCountInString(sample)), Unit: "chars", AccountID: h.Account.ID, Provider: h.Account.Provider, Model: h.Client.Model(), BYOK: h.BYOK, Note: "AI 生成简介"})
	}
	return intro, err
}

// TrashBook soft-deletes; RestoreBook brings it back within 30 days.
func (a *App) TrashBook(x *Access) error {
	if !(x.IsManager() || x.Book.CreatedBy == x.User.ID) {
		return Forbidden("只有图书创建者、所有者或管理员可以删除")
	}
	t := now()
	return a.DB.Model(x.Book).Update("deleted_at", &t).Error
}

func (a *App) PurgeTrash() {
	var books []model.Book
	a.DB.Where("deleted_at IS NOT NULL AND deleted_at < ?", now().Add(-30*24*time.Hour)).Find(&books)
	for _, b := range books {
		a.DeleteBookData(b.ID)
	}
}

// DeleteBookData permanently removes a book and its dependent rows.
func (a *App) DeleteBookData(bookID string) {
	// take/render audio files are removed by the orphan cleanup in cron
	for _, tbl := range []any{&model.Segment{}, &model.Script{}, &model.Character{}, &model.BookMember{}, &model.Render{}, &model.Recording{}, &model.ReviewRun{}, &model.ProductionTask{}, &model.Comment{}, &model.Issue{}, &model.ShareLink{}, &model.Export{}, &model.LexiconEntry{}} {
		a.DB.Where("book_id = ?", bookID).Delete(tbl)
	}
	a.DB.Where("chapter_id IN (?)", a.DB.Model(&model.Chapter{}).Select("id").Where("book_id = ?", bookID)).Delete(&model.Take{})
	a.DB.Where("chapter_id IN (?)", a.DB.Model(&model.Chapter{}).Select("id").Where("book_id = ?", bookID)).Delete(&model.Mix{})
	a.DB.Where("book_id = ?", bookID).Delete(&model.Chapter{})
	a.DB.Where("id = ?", bookID).Delete(&model.Book{})
}
