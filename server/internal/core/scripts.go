package core

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"

	dbx "voicebook/internal/db"
	"voicebook/internal/jobs"
	"voicebook/internal/model"
	"voicebook/internal/text"
)

// EnqueueScriptParse starts AI (or rule-only) script generation for chapters.
func (a *App) EnqueueScriptParse(x *Access, chapterIDs []string, mode string) ([]*model.Job, error) {
	if err := need(x, PermScript); err != nil {
		return nil, err
	}
	if mode != "rules" {
		mode = "ai"
		if _, err := a.LLMFor(FeatScript, x.Book.Settings.V.QualityTier, x.StudioID); err != nil {
			return nil, err
		}
	}
	var chs []model.Chapter
	a.DB.Where("book_id = ? AND id IN ?", x.Book.ID, chapterIDs).Order("\"index\" asc").Find(&chs)
	if len(chs) == 0 {
		return nil, Bad("请选择要生成画本的章节")
	}
	var words int
	for _, c := range chs {
		words += c.WordCount
	}
	if mode == "ai" {
		est := a.Estimate(FeatScript, "", float64(words))
		if b := a.Balance(x.StudioID); b.Available < est {
			return nil, PaymentRequired(fmt.Sprintf("积分不足：生成画本预计需要 %d 积分，当前可用 %d", est, b.Available), map[string]any{"need": est, "available": b.Available})
		}
	}
	var out []*model.Job
	for _, c := range chs {
		var running int64
		a.DB.Model(&model.Job{}).Where("chapter_id = ? AND type IN ? AND status IN ?", c.ID, []string{"script_parse", "script_sync"}, []string{model.JobQueued, model.JobRunning}).Count(&running)
		if running > 0 {
			continue
		}
		j := &model.Job{Type: "script_parse", StudioID: x.StudioID, BookID: x.Book.ID, ChapterID: c.ID, RefType: "chapter", RefID: c.ID,
			Title: "生成画本 · " + c.Title, CreatedBy: x.User.ID, Payload: model.Map{V: map[string]any{"mode": mode}}, MaxAttempts: 2}
		if err := a.Q.Enqueue(j); err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	if x.Book.Status == model.BookPreparing {
		a.DB.Model(x.Book).Update("status", model.BookProducing)
	}
	return out, nil
}

type segDraft struct {
	model.Segment
	unit text.Unit
}

// labelUnits converts units + labels into segment drafts, creating candidate characters for new speakers.
func (a *App) labelUnits(b *model.Book, units []text.Unit, res *AnnotateResult, chars []model.Character, drama bool) []model.Segment {
	_, codes := describeCharacters(chars)
	var narrator string
	byName := map[string]string{}
	for _, c := range chars {
		if c.Kind == model.CharNarrator && narrator == "" {
			narrator = c.ID
		}
		byName[c.Name] = c.ID
		for _, al := range c.Aliases.V {
			byName[al] = c.ID
		}
	}
	newChar := func(name string) string {
		name = strings.TrimSpace(name)
		if name == "" {
			return ""
		}
		if id, ok := byName[name]; ok {
			return id
		}
		c := model.Character{BookID: b.ID, Name: truncateStr(name, 60), Kind: model.CharMinor, Gender: "unknown", AgeGroup: "adult", Candidate: true, Mode: "ai",
			Aliases: model.Strings(), Color: charColors[len(byName)%len(charColors)], SortOrder: 900 + len(byName), Profile: "画本生成时发现的新角色，请确认后合并或保留。"}
		c.Params.V = model.VoiceParams{}
		c.Casting.V = []model.CastingCandidate{}
		if a.DB.Create(&c).Error != nil {
			return ""
		}
		byName[name] = c.ID
		return c.ID
	}
	var segs []model.Segment
	sfxAfter := map[int][]string{}
	if res != nil {
		for _, s := range res.Sfx {
			sfxAfter[s.AfterID] = append(sfxAfter[s.AfterID], s.Description)
		}
	}
	for _, u := range units {
		s := model.Segment{Para: u.Para, SceneBreak: u.SceneBreak, Text: u.Text, Seq: float64(u.Index+1) * 1000, Emotion: "neutral", Intensity: 1, Confidence: 1, AudioStatus: model.AudioNone, RecordStatus: model.RecTodo, Tags: model.Strings(), Effects: model.Strings()}
		s.Prons.V = []model.Pronunciation{}
		var lb UnitLabel
		ok := false
		if res != nil {
			lb, ok = res.Labels[u.Index]
		}
		if !ok {
			if u.Quote {
				s.Type = model.SegDialogue
				s.NeedsReview = true
				s.Confidence = 0
			} else {
				s.Type = model.SegNarration
				s.CharacterID = narrator
			}
			segs = append(segs, s)
			continue
		}
		s.Type = lb.Type
		if s.Type != model.SegDialogue && s.Type != model.SegMonologue {
			s.Type = model.SegNarration
		}
		sp := strings.TrimSpace(lb.Speaker)
		switch {
		case sp == "NARRATOR" || s.Type == model.SegNarration:
			s.CharacterID = narrator
			if s.Type != model.SegNarration && sp == "NARRATOR" {
				s.Type = model.SegNarration
			}
		case strings.HasPrefix(sp, "NEW:"):
			s.CharacterID = newChar(strings.TrimPrefix(sp, "NEW:"))
			s.NeedsReview = true
		case sp == "UNKNOWN" || sp == "":
			s.NeedsReview = true
		default:
			if id, ok := codes[sp]; ok {
				s.CharacterID = id
			} else if id, ok := byName[sp]; ok {
				s.CharacterID = id
			} else {
				s.NeedsReview = true
			}
		}
		if lb.Emotion != "" {
			s.Emotion = lb.Emotion
		}
		if lb.Intensity >= 1 && lb.Intensity <= 3 {
			s.Intensity = lb.Intensity
		}
		cues := CuesFromLLM(u.Text, lb.Cues, lb.Tags)
		if s.Type == model.SegNarration {
			cues = withoutSoundCues(cues) // the narrator describes actions, the character performs them
		}
		s.Cues = model.JSON[[]model.Cue]{V: cues}
		if !lb.SpeechTag {
			s.Instruction = CleanInstruction(lb.Instruction)
		}
		s.Confidence = lb.Confidence
		if s.Confidence < 0.6 {
			s.NeedsReview = true
		}
		if drama && b.Settings.V.RemoveSpeechTags && lb.SpeechTag && s.Type == model.SegNarration {
			s.Removed = true
		}
		for _, p := range lb.Prons {
			if p.Char == "" || p.Pinyin == "" {
				continue
			}
			wordAt := strings.Index(u.Text, p.Word)
			if p.Word == "" || wordAt < 0 {
				wordAt = 0
			}
			within := strings.Index(u.Text[wordAt:], p.Char)
			if within < 0 {
				continue
			}
			off := utf8.RuneCountInString(u.Text[:wordAt+within])
			s.Prons.V = append(s.Prons.V, model.Pronunciation{Offset: off, Char: p.Char, Pinyin: p.Pinyin, Homophone: firstNonEmpty(p.Homophone, text.Homophone(p.Pinyin))})
		}
		segs = append(segs, s)
		for k, desc := range sfxAfter[u.Index] {
			sfx := model.Segment{Para: u.Para, Type: model.SegSfx, Text: desc, Seq: s.Seq + 500 + float64(k), Emotion: "neutral", Intensity: 1, Confidence: 1, AudioStatus: model.AudioNone, Tags: model.Strings(), Effects: model.Strings()}
			sfx.Prons.V = []model.Pronunciation{}
			segs = append(segs, sfx)
		}
	}
	return segs
}

func (a *App) jobScriptParse(ctx context.Context, jc *jobs.Context) error {
	return a.scriptParse(ctx, jc.Job, jc, false)
}

// scriptParse generates a chapter script; shared by the job and the one-click pipeline.
func (a *App) scriptParse(ctx context.Context, j *model.Job, jc reporter, skipHold bool) error {
	var ch model.Chapter
	if err := a.DB.First(&ch, "id = ?", j.ChapterID).Error; err != nil {
		return jobs.Permanent(err)
	}
	var b model.Book
	a.DB.First(&b, "id = ?", ch.BookID)
	units := text.Segment(ch.SourceText, 120)
	if len(units) == 0 {
		return jobs.Permanent(errors.New("章节没有正文内容"))
	}
	var chars []model.Character
	a.DB.Where("book_id = ?", b.ID).Order("sort_order asc").Find(&chars)
	mode, _ := j.Payload.V["mode"].(string)
	drama := b.Mode == model.ModeDrama
	var res *AnnotateResult
	var h *LLMHandle
	if mode != "rules" {
		var err error
		h, err = a.LLMFor(FeatScript, b.Settings.V.QualityTier, b.StudioID)
		if err != nil {
			return jobs.Permanent(err)
		}
		if !skipHold {
			if err := a.Hold(j.ID, b.StudioID, a.Estimate(FeatScript, "", float64(ch.WordCount))); err != nil {
				return jobs.Permanent(err)
			}
		}
		res, err = a.AnnotateUnits(ctx, h, units, chars, drama, func(p float64, t string) { jc.Progress(0.05+p*0.8, t) })
		if err != nil {
			return err
		}
	}
	jc.Progress(0.88, "正在整理画本")
	segs := a.labelUnits(&b, units, res, chars, drama)
	script, err := a.saveNewScript(&ch, segs, map[bool]string{true: "rules", false: "ai"}[mode == "rules"], j.CreatedBy, j.ID)
	if err != nil {
		return err
	}
	if drama {
		a.matchSfxForScript(ctx, &b, script.ID)
	}
	if res != nil {
		if _, err := a.ChargeCredits(Charge{StudioID: b.StudioID, UserID: j.CreatedBy, BookID: b.ID, ChapterID: ch.ID, JobID: j.ID, Feature: FeatScript, Quantity: float64(res.Chars), Unit: "chars", AccountID: h.Account.ID, Provider: h.Account.Provider, Model: h.Client.Model(), BYOK: h.BYOK}); err != nil {
			return jobs.Permanent(err)
		}
	}
	var review int64
	a.DB.Model(&model.Segment{}).Where("script_id = ? AND needs_review = ?", script.ID, true).Count(&review)
	jc.SetResult(map[string]any{"scriptId": script.ID, "segments": len(segs), "needsReview": review})
	jc.Progress(1, fmt.Sprintf("画本已生成：%d 个片段，%d 处需要确认", len(segs), review))
	a.Activity(b.StudioID, b.ID, j.CreatedBy, "script.parse", "chapter", ch.ID, fmt.Sprintf("生成了「%s」的画本（v%d）", ch.Title, script.Version))
	return nil
}

// saveNewScript stores segments as a new current version, carrying over locked lines and reusable audio.
func (a *App) saveNewScript(ch *model.Chapter, segs []model.Segment, origin, userID, jobID string) (*model.Script, error) {
	var prev []model.Segment
	if ch.CurrentScriptID != "" {
		a.DB.Where("script_id = ?", ch.CurrentScriptID).Order("seq asc").Find(&prev)
	}
	var maxV int
	a.DB.Model(&model.Script{}).Where("chapter_id = ?", ch.ID).Select("COALESCE(MAX(version),0)").Scan(&maxV)
	script := &model.Script{ChapterID: ch.ID, BookID: ch.BookID, Version: maxV + 1, Origin: origin, Status: "ready", JobID: jobID, CreatedBy: userID, Stats: model.EmptyMap()}
	// index previous segments by text for carry-over
	prevByText := map[string][]*model.Segment{}
	for i := range prev {
		prevByText[prev[i].Text] = append(prevByText[prev[i].Text], &prev[i])
	}
	err := a.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(script).Error; err != nil {
			return err
		}
		for i := range segs {
			s := &segs[i]
			s.ID = ""
			s.ScriptID, s.ChapterID, s.BookID = script.ID, ch.ID, ch.BookID
			oldSegID := ""
			if cands := prevByText[s.Text]; len(cands) > 0 {
				old := cands[0]
				prevByText[s.Text] = cands[1:]
				if old.Locked || old.Modified {
					// keep the user's work verbatim
					keepSeq, keepPara, keepScene := s.Seq, s.Para, s.SceneBreak
					*s = *old
					s.ID, s.Seq, s.Para, s.SceneBreak = "", keepSeq, keepPara, keepScene
					s.ScriptID, s.ChapterID, s.BookID = script.ID, ch.ID, ch.BookID
				} else if old.CharacterID == s.CharacterID && old.SelectedTakeID != "" {
					s.SelectedTakeID, s.AudioStatus, s.ContentHash, s.RecordStatus = old.SelectedTakeID, old.AudioStatus, old.ContentHash, old.RecordStatus
				}
				oldSegID = old.ID
			}
			if err := tx.Create(s).Error; err != nil {
				return err
			}
			if oldSegID != "" && s.SelectedTakeID != "" {
				// duplicate takes so history stays per version
				var takes []model.Take
				tx.Where("segment_id = ?", oldSegID).Find(&takes)
				for _, t := range takes {
					oldID := t.ID
					t.ID = ""
					t.SegmentID = s.ID
					if err := tx.Create(&t).Error; err != nil {
						return err
					}
					if oldID == s.SelectedTakeID {
						tx.Model(s).Update("selected_take_id", t.ID)
					}
				}
			}
		}
		return tx.Model(ch).Updates(map[string]any{"current_script_id": script.ID, "script_stale": false, "render_stale": true}).Error
	})
	if err != nil {
		return nil, err
	}
	ch.CurrentScriptID = script.ID
	a.RecountBook(ch.BookID)
	a.EnsureChapterTasks(ch.BookID)
	var st model.Book
	a.DB.Select("studio_id").First(&st, "id = ?", ch.BookID)
	a.emit(st.StudioID, eventChapter(ch.BookID, ch.ID, "script"))
	return script, nil
}

// matchSfxForScript assigns library sound effects to sfx cues (LLM pick, keyword fallback).
func (a *App) matchSfxForScript(ctx context.Context, b *model.Book, scriptID string) {
	var cues []model.Segment
	a.DB.Where("script_id = ? AND type = ? AND sfx_asset_id = ''", scriptID, model.SegSfx).Find(&cues)
	if len(cues) == 0 {
		return
	}
	var assets []model.Asset
	a.DB.Where("kind IN ? AND ((scope = ? AND listed = ?) OR studio_id = ?)", []string{model.AssetSFX, model.AssetAmbience}, model.ScopeSystem, true, b.StudioID).Find(&assets)
	if len(assets) == 0 {
		return
	}
	h, _ := a.LLMFor(FeatScript, "", b.StudioID)
	for _, c := range cues {
		cands := rankSfx(c.Text, assets, 40) // the library has hundreds of sounds: offer the closest few dozen
		if len(cands) == 0 {
			continue
		}
		var pick string
		if h != nil {
			if ch, err := a.MatchSFX(ctx, h, c.Text, cands); err == nil {
				pick = ch.AssetID
			}
		}
		if pick == "" {
			pick = keywordAsset(c.Text, cands)
		}
		if pick != "" {
			a.DB.Model(&model.Segment{}).Where("id = ?", c.ID).Update("sfx_asset_id", pick)
		}
	}
}

// keywordAsset is the fallback when no model is available: the best-ranked sound, if the match is
// more than a stray shared character.
func keywordAsset(desc string, assets []model.Asset) string {
	best := rankSfx(desc, assets, 1)
	if len(best) == 0 || sfxScore(desc, best[0]) < 6 {
		return ""
	}
	return best[0].ID
}

// ---------- sync script after source edits ----------

func (a *App) EnqueueScriptSync(x *Access, ch *model.Chapter) (*model.Job, error) {
	if err := need(x, PermScript); err != nil {
		return nil, err
	}
	if ch.CurrentScriptID == "" {
		return nil, Bad("该章节还没有画本")
	}
	j := &model.Job{Type: "script_sync", StudioID: x.StudioID, BookID: ch.BookID, ChapterID: ch.ID, RefType: "chapter", RefID: ch.ID, Title: "同步画本 · " + ch.Title, CreatedBy: x.User.ID, MaxAttempts: 2}
	return j, a.Q.Enqueue(j)
}

func (a *App) jobScriptSync(ctx context.Context, jc *jobs.Context) error {
	j := jc.Job
	var ch model.Chapter
	if err := a.DB.First(&ch, "id = ?", j.ChapterID).Error; err != nil {
		return jobs.Permanent(err)
	}
	var b model.Book
	a.DB.First(&b, "id = ?", ch.BookID)
	var old []model.Segment
	a.DB.Where("script_id = ?", ch.CurrentScriptID).Order("seq asc").Find(&old)
	// reconstruct old paragraphs from segments
	byPara := map[int][]model.Segment{}
	var oldParas []string
	maxPara := -1
	for _, s := range old {
		if s.Type == model.SegSfx || s.Type == model.SegPause {
			continue
		}
		byPara[s.Para] = append(byPara[s.Para], s)
		if s.Para > maxPara {
			maxPara = s.Para
		}
	}
	paraIdx := []int{}
	for p := 0; p <= maxPara; p++ {
		if segs, ok := byPara[p]; ok {
			var sb strings.Builder
			for _, s := range segs {
				sb.WriteString(s.Text)
			}
			oldParas = append(oldParas, sb.String())
			paraIdx = append(paraIdx, p)
		}
	}
	newParas, breaks := text.Paragraphs(ch.SourceText)
	ops := text.DiffStrings(oldParas, newParas)
	// units for new/changed paragraphs
	var fresh []text.Unit
	keep := map[int]int{} // new para -> old para
	for _, o := range ops {
		if o.Kind == "equal" {
			keep[o.B] = paraIdx[o.A]
		}
	}
	for pi, p := range newParas {
		if _, ok := keep[pi]; ok {
			continue
		}
		for _, u := range text.Segment(p, 120) {
			u.Para = pi
			if len(fresh) == 0 || fresh[len(fresh)-1].Para != pi {
				u.SceneBreak = breaks[pi]
			}
			fresh = append(fresh, u)
		}
	}
	for i := range fresh {
		fresh[i].Index = i
	}
	var chars []model.Character
	a.DB.Where("book_id = ?", b.ID).Order("sort_order asc").Find(&chars)
	var res *AnnotateResult
	var h *LLMHandle
	if len(fresh) > 0 {
		if hh, err := a.LLMFor(FeatScript, b.Settings.V.QualityTier, b.StudioID); err == nil {
			h = hh
			res, err = a.AnnotateUnits(ctx, h, fresh, chars, b.Mode == model.ModeDrama, func(p float64, t string) { jc.Progress(p*0.8, t) })
			if err != nil {
				return err
			}
		}
	}
	labeled := a.labelUnits(&b, fresh, res, chars, b.Mode == model.ModeDrama)
	for i := range labeled {
		labeled[i].NeedsReview = true
	}
	freshByPara := map[int][]model.Segment{}
	for _, s := range labeled {
		freshByPara[s.Para] = append(freshByPara[s.Para], s)
	}
	var segs []model.Segment
	seq := 1000.0
	for pi := range newParas {
		var list []model.Segment
		if op, ok := keep[pi]; ok {
			for _, s := range byPara[op] {
				s.Para = pi
				list = append(list, s)
			}
			for _, s := range old { // keep sfx cues attached to kept paragraphs
				if s.Type == model.SegSfx && s.Para == op {
					s.Para = pi
					list = append(list, s)
				}
			}
			sort.SliceStable(list, func(i, j int) bool { return list[i].Seq < list[j].Seq })
		} else {
			list = freshByPara[pi]
		}
		for _, s := range list {
			s.Seq = seq
			seq += 1000
			segs = append(segs, s)
		}
	}
	script, err := a.saveSyncedScript(&ch, segs, j.CreatedBy, j.ID)
	if err != nil {
		return err
	}
	if res != nil {
		a.ChargeCredits(Charge{StudioID: b.StudioID, UserID: j.CreatedBy, BookID: b.ID, ChapterID: ch.ID, JobID: j.ID, Feature: FeatScript, Quantity: float64(res.Chars), Unit: "chars", AccountID: h.Account.ID, Provider: h.Account.Provider, Model: h.Client.Model(), BYOK: h.BYOK})
	}
	jc.Progress(1, fmt.Sprintf("画本已同步：保留 %d 段，更新 %d 段", len(keep), len(newParas)-len(keep)))
	jc.SetResult(map[string]any{"scriptId": script.ID})
	return nil
}

// saveSyncedScript is like saveNewScript but keeps the identity of carried segments' takes.
func (a *App) saveSyncedScript(ch *model.Chapter, segs []model.Segment, userID, jobID string) (*model.Script, error) {
	var maxV int
	a.DB.Model(&model.Script{}).Where("chapter_id = ?", ch.ID).Select("COALESCE(MAX(version),0)").Scan(&maxV)
	script := &model.Script{ChapterID: ch.ID, BookID: ch.BookID, Version: maxV + 1, Origin: "sync", Status: "ready", JobID: jobID, CreatedBy: userID, Stats: model.EmptyMap()}
	err := a.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(script).Error; err != nil {
			return err
		}
		for i := range segs {
			s := segs[i]
			oldID := s.ID
			s.ID = ""
			s.ScriptID, s.ChapterID, s.BookID = script.ID, ch.ID, ch.BookID
			if err := tx.Create(&s).Error; err != nil {
				return err
			}
			if oldID != "" {
				var takes []model.Take
				tx.Where("segment_id = ?", oldID).Find(&takes)
				for _, t := range takes {
					prevID := t.ID
					t.ID = ""
					t.SegmentID = s.ID
					tx.Create(&t)
					if prevID == s.SelectedTakeID {
						tx.Model(&s).Update("selected_take_id", t.ID)
					}
				}
			}
		}
		return tx.Model(ch).Updates(map[string]any{"current_script_id": script.ID, "script_stale": false, "render_stale": true}).Error
	})
	if err != nil {
		return nil, err
	}
	a.RecountBook(ch.BookID)
	return script, nil
}

// ---------- segment editing ----------

type SegmentPatch struct {
	Text          *string                `json:"text"`
	CharacterID   *string                `json:"characterId"`
	Type          *string                `json:"type"`
	Emotion       *string                `json:"emotion"`
	Intensity     *int                   `json:"intensity"`
	Tags          *[]string              `json:"tags"`
	Speed         *float64               `json:"speed"`
	Volume        *float64               `json:"volume"`
	Pitch         *float64               `json:"pitch"`
	PauseBeforeMs *int                   `json:"pauseBeforeMs"`
	PauseAfterMs  *int                   `json:"pauseAfterMs"`
	ClearPauses   bool                   `json:"clearPauses"`
	ClearProsody  bool                   `json:"clearProsody"`
	Instruction   *string                `json:"instruction"`
	Prons         *[]model.Pronunciation `json:"prons"`
	Cues          *[]model.Cue           `json:"cues"`
	Effects       *[]string              `json:"effects"`
	SfxAssetID    *string                `json:"sfxAssetId"`
	Locked        *bool                  `json:"locked"`
	Removed       *bool                  `json:"removed"`
	NeedsReview   *bool                  `json:"needsReview"`
	Note          *string                `json:"note"`
}

// audioAffecting reports whether the patch changes how the line sounds.
func (p SegmentPatch) audioAffecting() bool {
	return p.Text != nil || p.CharacterID != nil || p.Emotion != nil || p.Intensity != nil || p.Tags != nil || p.Speed != nil ||
		p.Pitch != nil || p.Instruction != nil || p.Prons != nil || p.Cues != nil || p.ClearProsody
}

func (a *App) UpdateSegments(x *Access, ids []string, p SegmentPatch) ([]model.Segment, error) {
	if len(ids) == 0 {
		return nil, Bad("没有选择片段")
	}
	// permission: script editors can change anything; post can change pauses/effects/volume; voice-only can't edit
	scriptFields := p.Text != nil || p.CharacterID != nil || p.Type != nil || p.Emotion != nil || p.Intensity != nil || p.Tags != nil || p.Instruction != nil || p.Prons != nil || p.Cues != nil || p.Removed != nil || p.Locked != nil || p.NeedsReview != nil
	if scriptFields {
		if err := need(x, PermScript); err != nil {
			return nil, err
		}
	} else if !x.Can(PermScript) && !x.Can(PermPost) {
		return nil, Forbidden("")
	}
	var segs []model.Segment
	a.DB.Where("id IN ? AND book_id = ?", ids, x.Book.ID).Find(&segs)
	if len(segs) == 0 {
		return nil, NotFound("片段")
	}
	if p.CharacterID != nil && *p.CharacterID != "" {
		var c model.Character
		if a.DB.First(&c, "id = ? AND book_id = ?", *p.CharacterID, x.Book.ID).Error != nil {
			return nil, Bad("角色不存在")
		}
	}
	up := map[string]any{}
	if p.Text != nil {
		t := *p.Text
		if strings.TrimSpace(t) == "" {
			return nil, Bad("片段内容不能为空")
		}
		up["text"] = t
		up["modified"] = true
	}
	if p.CharacterID != nil {
		up["character_id"] = *p.CharacterID
		up["needs_review"] = false
	}
	if p.Type != nil {
		up["type"] = *p.Type
	}
	if p.Emotion != nil {
		up["emotion"] = *p.Emotion
	}
	if p.Intensity != nil {
		up["intensity"] = *p.Intensity
	}
	if p.Tags != nil {
		up["tags"] = model.Strings(*p.Tags...)
	}
	if p.ClearProsody {
		up["speed"], up["volume"], up["pitch"] = nil, nil, nil
	}
	if p.Speed != nil {
		up["speed"] = *p.Speed
	}
	if p.Volume != nil {
		up["volume"] = *p.Volume
	}
	if p.Pitch != nil {
		up["pitch"] = *p.Pitch
	}
	if p.ClearPauses {
		up["pause_before_ms"], up["pause_after_ms"] = nil, nil
	}
	if p.PauseBeforeMs != nil {
		up["pause_before_ms"] = *p.PauseBeforeMs
	}
	if p.PauseAfterMs != nil {
		up["pause_after_ms"] = *p.PauseAfterMs
	}
	if p.Instruction != nil {
		up["instruction"] = truncateStr(*p.Instruction, 490)
	}
	if p.Prons != nil {
		up["prons"] = model.JSON[[]model.Pronunciation]{V: *p.Prons}
	}
	if p.Effects != nil {
		up["effects"] = model.Strings(*p.Effects...)
	}
	if p.SfxAssetID != nil {
		up["sfx_asset_id"] = *p.SfxAssetID
	}
	if p.Locked != nil {
		up["locked"] = *p.Locked
	}
	if p.Removed != nil {
		up["removed"] = *p.Removed
	}
	if p.NeedsReview != nil {
		up["needs_review"] = *p.NeedsReview
	}
	if p.Note != nil {
		up["note"] = truncateStr(*p.Note, 490)
	}
	if len(up) == 0 && p.Cues == nil { // cues are resolved per line below
		return segs, nil
	}
	for _, s := range segs {
		if s.Locked && p.Locked == nil && scriptFields {
			continue // locked lines are protected from batch edits
		}
		u := map[string]any{}
		for k, v := range up {
			u[k] = v
		}
		if p.audioAffecting() && (s.AudioStatus == model.AudioReady) {
			u["audio_status"] = model.AudioStale
		}
		if p.Text != nil && s.Type != model.SegSfx {
			u["qc"] = ""
		}
		// keep pronunciations and performance cues attached to the right characters after a text edit
		newText := s.Text
		if p.Text != nil {
			newText = *p.Text
		}
		prons, cues := s.Prons.V, s.Cues.V
		if p.Prons != nil {
			prons = *p.Prons
		}
		if p.Cues != nil {
			cues = *p.Cues
		}
		if p.Text != nil {
			// annotations sent with the new text already refer to it; only carry over the old ones
			rp, rc := RemapAnnotations(s.Text, newText, prons, cues)
			if p.Prons == nil {
				prons = rp
			}
			if p.Cues == nil {
				cues = rc
			}
			u["prons"] = model.JSON[[]model.Pronunciation]{V: nonNilProns(prons)}
		}
		if p.Text != nil || p.Cues != nil {
			u["cues"] = model.JSON[[]model.Cue]{V: NormalizeSegmentCues(cues, newText)}
		}
		a.DB.Model(&model.Segment{}).Where("id = ?", s.ID).Updates(u)
	}
	a.DB.Model(&model.Chapter{}).Where("id IN (?)", a.DB.Model(&model.Segment{}).Select("chapter_id").Where("id IN ?", ids)).Update("render_stale", true)
	var out []model.Segment
	a.DB.Where("id IN ?", ids).Order("seq asc").Find(&out)
	if len(out) > 0 {
		a.emit(x.StudioID, eventSegments(x.Book.ID, out[0].ChapterID, a.SegmentViews(out)))
		if p.CharacterID != nil {
			a.RecountBook(x.Book.ID)
		}
	}
	return out, nil
}

func (a *App) scriptSegments(scriptID string) []model.Segment {
	var segs []model.Segment
	a.DB.Where("script_id = ?", scriptID).Order("seq asc").Find(&segs)
	return segs
}

// SplitSegment splits a line at a rune offset.
func (a *App) SplitSegment(x *Access, s *model.Segment, at int) ([]model.Segment, error) {
	if err := need(x, PermScript); err != nil {
		return nil, err
	}
	r := []rune(s.Text)
	if at <= 0 || at >= len(r) {
		return nil, Bad("请把光标放在需要拆分的位置")
	}
	segs := a.scriptSegments(s.ScriptID)
	next := s.Seq + 1000
	for i, x := range segs {
		if x.ID == s.ID && i+1 < len(segs) {
			next = segs[i+1].Seq
		}
	}
	second := *s
	second.ID = ""
	second.Text = string(r[at:])
	second.Seq = (s.Seq + next) / 2
	second.SelectedTakeID = ""
	second.AudioStatus = model.AudioNone
	second.Modified = true
	var p1, p2 []model.Pronunciation
	for _, p := range s.Prons.V {
		if p.Offset < at {
			p1 = append(p1, p)
		} else {
			p.Offset -= at
			p2 = append(p2, p)
		}
	}
	second.Prons = model.JSON[[]model.Pronunciation]{V: nonNilProns(p2)}
	var c1, c2 []model.Cue
	for _, c := range s.Cues.V {
		if c.At <= at {
			c1 = append(c1, c)
		} else {
			c.At -= at
			c2 = append(c2, c)
		}
	}
	second.Cues = model.JSON[[]model.Cue]{V: NormalizeSegmentCues(c2, second.Text)}
	err := a.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(s).Updates(map[string]any{"text": string(r[:at]), "modified": true, "prons": model.JSON[[]model.Pronunciation]{V: nonNilProns(p1)}, "cues": model.JSON[[]model.Cue]{V: NormalizeSegmentCues(c1, string(r[:at]))}, "audio_status": staleIfReady(s.AudioStatus)}).Error; err != nil {
			return err
		}
		return tx.Create(&second).Error
	})
	if err != nil {
		return nil, err
	}
	a.markRenderStale(s.ChapterID)
	a.emit(x.StudioID, eventChapter(s.BookID, s.ChapterID, "script"))
	return a.scriptSegments(s.ScriptID), nil
}

func nonNilCues(c []model.Cue) []model.Cue {
	if c == nil {
		return []model.Cue{}
	}
	return c
}

func nonNilProns(p []model.Pronunciation) []model.Pronunciation {
	if p == nil {
		return []model.Pronunciation{}
	}
	return p
}

func staleIfReady(st string) string {
	if st == model.AudioReady {
		return model.AudioStale
	}
	return st
}

// MergeSegments merges consecutive lines into the first one.
func (a *App) MergeSegments(x *Access, ids []string) ([]model.Segment, error) {
	if err := need(x, PermScript); err != nil {
		return nil, err
	}
	var segs []model.Segment
	a.DB.Where("id IN ? AND book_id = ?", ids, x.Book.ID).Order("seq asc").Find(&segs)
	if len(segs) < 2 {
		return nil, Bad("请至少选择两个相邻片段")
	}
	first := segs[0]
	var sb strings.Builder
	var prons []model.Pronunciation
	var cues []model.Cue
	offset := 0
	for _, s := range segs {
		if s.ScriptID != first.ScriptID {
			return nil, Bad("只能合并同一章节中的片段")
		}
		for _, p := range s.Prons.V {
			p.Offset += offset
			prons = append(prons, p)
		}
		for _, c := range s.Cues.V {
			c.At += offset
			cues = append(cues, c)
		}
		sb.WriteString(s.Text)
		offset += utf8.RuneCountInString(s.Text)
	}
	err := a.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&first).Updates(map[string]any{"text": sb.String(), "modified": true, "prons": model.JSON[[]model.Pronunciation]{V: nonNilProns(prons)}, "cues": model.JSON[[]model.Cue]{V: NormalizeSegmentCues(cues, sb.String())}, "audio_status": staleIfReady(first.AudioStatus)}).Error; err != nil {
			return err
		}
		for _, s := range segs[1:] {
			tx.Delete(&model.Segment{}, "id = ?", s.ID)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	a.markRenderStale(first.ChapterID)
	a.emit(x.StudioID, eventChapter(first.BookID, first.ChapterID, "script"))
	return a.scriptSegments(first.ScriptID), nil
}

// InsertSegment adds a new line after `afterID` (or at the start when empty).
func (a *App) InsertSegment(x *Access, ch *model.Chapter, afterID, typ, txt, characterID string) ([]model.Segment, error) {
	if err := need(x, PermScript); err != nil {
		return nil, err
	}
	if ch.CurrentScriptID == "" {
		return nil, Bad("请先生成画本")
	}
	segs := a.scriptSegments(ch.CurrentScriptID)
	seq, para, found := 1000.0, 0, afterID == ""
	for i, s := range segs {
		if s.ID == afterID {
			next := s.Seq + 1000
			if i+1 < len(segs) {
				next = segs[i+1].Seq
			}
			seq, para, found = (s.Seq+next)/2, s.Para, true
			if next-s.Seq <= 1e-6 { // gap exhausted by repeated inserts: renumber, then insert between
				for k := range segs {
					segs[k].Seq = float64(k+1) * 1000
					a.DB.Model(&model.Segment{}).Where("id = ?", segs[k].ID).Update("seq", segs[k].Seq)
				}
				seq = segs[i].Seq + 500
			}
		}
	}
	if !found {
		return nil, NotFound("插入位置的句子（可能已被删除，请刷新后再试）")
	}
	if afterID == "" && len(segs) > 0 {
		seq, para = segs[0].Seq-1000, segs[0].Para
	}
	if typ == "" {
		typ = model.SegNarration
	}
	if strings.TrimSpace(txt) == "" && typ != model.SegPause {
		txt = "（新片段）"
	}
	s := model.Segment{ScriptID: ch.CurrentScriptID, ChapterID: ch.ID, BookID: ch.BookID, Seq: seq, Para: para, Type: typ, Text: txt, CharacterID: characterID,
		Emotion: "neutral", Intensity: 1, Confidence: 1, AudioStatus: model.AudioNone, RecordStatus: model.RecTodo, Modified: true, Tags: model.Strings(), Effects: model.Strings()}
	s.Prons.V = []model.Pronunciation{}
	if typ == model.SegPause {
		ms := 1000
		s.PauseAfterMs = &ms
		s.Text = "（停顿）"
	}
	if err := a.DB.Create(&s).Error; err != nil {
		return nil, err
	}
	a.markRenderStale(ch.ID)
	a.RecountBook(ch.BookID)
	a.emit(x.StudioID, eventChapter(ch.BookID, ch.ID, "script"))
	return a.scriptSegments(ch.CurrentScriptID), nil
}

// DeleteSegments removes lines from a script and returns them so the editor can offer undo.
// Locked lines are kept and reported.
func (a *App) DeleteSegments(x *Access, ids []string) ([]model.Segment, int, error) {
	if err := need(x, PermScript); err != nil {
		return nil, 0, err
	}
	if len(ids) == 0 {
		return nil, 0, Bad("没有选择要删除的句子")
	}
	var segs []model.Segment
	a.DB.Where("id IN ? AND book_id = ?", ids, x.Book.ID).Find(&segs)
	var del []model.Segment
	locked := 0
	for _, s := range segs {
		if s.Locked {
			locked++
			continue
		}
		del = append(del, s)
	}
	if len(del) == 0 {
		if locked > 0 {
			return nil, locked, Bad("所选句子已锁定，请先解除锁定再删除")
		}
		return nil, 0, NotFound("片段")
	}
	delIDs := make([]string, 0, len(del))
	chapters := map[string]bool{}
	for _, s := range del {
		delIDs = append(delIDs, s.ID)
		chapters[s.ChapterID] = true
	}
	if err := a.DB.Where("id IN ?", delIDs).Delete(&model.Segment{}).Error; err != nil {
		return nil, 0, err
	}
	for c := range chapters {
		a.markRenderStale(c)
		a.emit(x.StudioID, eventChapter(x.Book.ID, c, "script"))
	}
	a.RecountBook(x.Book.ID)
	return del, locked, nil
}

// RestoreSegments re-creates lines removed by DeleteSegments (undo). Audio takes were kept, so the
// previously selected take comes back too.
func (a *App) RestoreSegments(x *Access, segs []model.Segment) (int, error) {
	if err := need(x, PermScript); err != nil {
		return 0, err
	}
	n := 0
	chapters := map[string]bool{}
	for _, s := range segs {
		if s.ID == "" || s.ScriptID == "" {
			continue
		}
		var sc model.Script
		if a.DB.First(&sc, "id = ? AND book_id = ?", s.ScriptID, x.Book.ID).Error != nil {
			continue
		}
		var exists int64
		a.DB.Model(&model.Segment{}).Where("id = ?", s.ID).Count(&exists)
		if exists > 0 {
			continue
		}
		s.BookID, s.ChapterID = x.Book.ID, sc.ChapterID
		if s.SelectedTakeID != "" {
			var t int64
			a.DB.Model(&model.Take{}).Where("id = ? AND segment_id = ?", s.SelectedTakeID, s.ID).Count(&t)
			if t == 0 {
				s.SelectedTakeID, s.AudioStatus = "", model.AudioNone
			}
		}
		s.Cues = model.JSON[[]model.Cue]{V: NormalizeSegmentCues(s.Cues.V, s.Text)}
		if a.DB.Create(&s).Error == nil {
			n++
			chapters[s.ChapterID] = true
		}
	}
	for c := range chapters {
		a.markRenderStale(c)
		a.emit(x.StudioID, eventChapter(x.Book.ID, c, "script"))
	}
	a.RecountBook(x.Book.ID)
	return n, nil
}

// MoveSegment moves a line one visible position up (dir < 0) or down (dir > 0).
func (a *App) MoveSegment(x *Access, s *model.Segment, dir int) ([]model.Segment, error) {
	if err := need(x, PermScript); err != nil {
		return nil, err
	}
	var vis []model.Segment // lines the editor shows (speech tags dropped in drama mode stay where they are)
	for _, g := range a.scriptSegments(s.ScriptID) {
		if !g.Removed || g.ID == s.ID {
			vis = append(vis, g)
		}
	}
	i := -1
	for k := range vis {
		if vis[k].ID == s.ID {
			i = k
		}
	}
	switch {
	case i < 0 || dir == 0 || (dir < 0 && i == 0) || (dir > 0 && i == len(vis)-1):
		return a.scriptSegments(s.ScriptID), nil
	case dir < 0 && i == 1:
		return a.MoveSegmentAfter(x, s, "")
	case dir < 0:
		return a.MoveSegmentAfter(x, s, vis[i-2].ID)
	default:
		return a.MoveSegmentAfter(x, s, vis[i+1].ID)
	}
}

// MoveSegmentAfter places a line right after afterID ("" = first line of the chapter). The line joins
// the paragraph of the line it now follows (or, at the very start, the one it precedes), so paragraph
// breaks stay where the editor draws them.
func (a *App) MoveSegmentAfter(x *Access, s *model.Segment, afterID string) ([]model.Segment, error) {
	if err := need(x, PermScript); err != nil {
		return nil, err
	}
	segs := a.scriptSegments(s.ScriptID)
	if afterID == s.ID {
		return segs, nil
	}
	rest := make([]model.Segment, 0, len(segs))
	for _, g := range segs {
		if g.ID != s.ID {
			rest = append(rest, g)
		}
	}
	if len(rest) == 0 {
		return segs, nil
	}
	pos := 0 // index in rest the line moves to
	if afterID != "" {
		pos = -1
		for i, g := range rest {
			if g.ID == afterID {
				pos = i + 1
			}
		}
		if pos < 0 {
			return nil, NotFound("目标位置的句子（可能已被删除，请刷新后再试）")
		}
	}
	var prev, next float64
	switch {
	case pos == 0:
		next = rest[0].Seq
		prev = next - 1000
	case pos == len(rest):
		prev = rest[pos-1].Seq
		next = prev + 1000
	default:
		prev, next = rest[pos-1].Seq, rest[pos].Seq
	}
	para := rest[0].Para
	if pos > 0 {
		para = rest[pos-1].Para
	}
	err := a.DB.Transaction(func(tx *gorm.DB) error {
		if next-prev > 1e-6 {
			return tx.Model(&model.Segment{}).Where("id = ?", s.ID).Updates(map[string]any{"seq": (prev + next) / 2, "para": para}).Error
		}
		// repeated moves into the same gap exhausted float precision: renumber the whole script
		order := append(append(append([]model.Segment{}, rest[:pos]...), *s), rest[pos:]...)
		for i, g := range order {
			up := map[string]any{"seq": float64(i+1) * 1000}
			if g.ID == s.ID {
				up["para"] = para
			}
			if err := tx.Model(&model.Segment{}).Where("id = ?", g.ID).Updates(up).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	a.markRenderStale(s.ChapterID)
	a.emit(x.StudioID, eventChapter(s.BookID, s.ChapterID, "script"))
	return a.scriptSegments(s.ScriptID), nil
}

// StaleLinesWithTerms marks voiced lines of the book's current scripts that contain any of the terms
// as changed, so "regenerate changed lines" picks them up after a reading in the lexicon changes.
// It returns how many lines were affected.
func (a *App) StaleLinesWithTerms(bookID string, terms ...string) int {
	seen := map[string]bool{}
	var segs []model.Segment
	for _, t := range terms {
		t = strings.TrimSpace(t)
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		var list []model.Segment
		a.DB.Select("id, chapter_id, audio_status").Where("book_id = ? AND removed = ? AND "+dbx.Like(a.DB, "text"), bookID, false, "%"+t+"%").
			Where("script_id IN (?)", a.DB.Model(&model.Chapter{}).Select("current_script_id").Where("book_id = ?", bookID)).Find(&list)
		segs = append(segs, list...)
	}
	var ids []string
	chapters := map[string]bool{}
	done := map[string]bool{}
	for _, s := range segs {
		if s.AudioStatus != model.AudioReady || done[s.ID] {
			continue
		}
		done[s.ID] = true
		ids = append(ids, s.ID)
		chapters[s.ChapterID] = true
	}
	if len(ids) == 0 {
		return 0
	}
	a.DB.Model(&model.Segment{}).Where("id IN ?", ids).Update("audio_status", model.AudioStale)
	var b model.Book
	a.DB.Select("id, studio_id").First(&b, "id = ?", bookID)
	for cid := range chapters {
		a.markRenderStale(cid)
		a.emit(b.StudioID, eventChapter(bookID, cid, "script"))
	}
	return len(ids)
}

func (a *App) markRenderStale(chapterID string) {
	a.DB.Model(&model.Chapter{}).Where("id = ? AND render_id <> ''", chapterID).Update("render_stale", true)
}

// ReassignCharacter moves all lines of one character to another within chapters (or the whole book).
func (a *App) ReassignCharacter(x *Access, chapterID, fromID, toID string) (int64, error) {
	if err := need(x, PermScript); err != nil {
		return 0, err
	}
	q := a.DB.Model(&model.Segment{}).Where("book_id = ? AND character_id = ? AND locked = ?", x.Book.ID, fromID, false).
		Where("script_id IN (?)", a.DB.Model(&model.Chapter{}).Select("current_script_id").Where("book_id = ?", x.Book.ID))
	if chapterID != "" {
		q = q.Where("chapter_id = ?", chapterID)
	}
	res := q.Updates(map[string]any{"character_id": toID, "needs_review": false, "audio_status": gorm.Expr("CASE WHEN audio_status = ? THEN ? ELSE audio_status END", model.AudioReady, model.AudioStale)})
	a.RecountBook(x.Book.ID)
	return res.RowsAffected, res.Error
}

// FindReplace replaces text across the chapter or book's current scripts.
func (a *App) FindReplace(x *Access, chapterID, find, replace string, apply bool) (int, []map[string]any, error) {
	if err := need(x, PermScript); err != nil {
		return 0, nil, err
	}
	if find == "" {
		return 0, nil, Bad("请输入要查找的内容")
	}
	q := a.DB.Where("book_id = ? AND locked = ?", x.Book.ID, false).Where("script_id IN (?)", a.DB.Model(&model.Chapter{}).Select("current_script_id").Where("book_id = ?", x.Book.ID)).
		Where(dbx.Like(a.DB, "text"), "%"+find+"%")
	if chapterID != "" {
		q = q.Where("chapter_id = ?", chapterID)
	}
	var segs []model.Segment
	q.Order("seq asc").Find(&segs)
	var matches []map[string]any
	count := 0
	for _, s := range segs {
		n := strings.Count(s.Text, find)
		count += n
		if len(matches) < 200 {
			matches = append(matches, map[string]any{"segmentId": s.ID, "chapterId": s.ChapterID, "text": s.Text, "count": n})
		}
		if apply {
			a.DB.Model(&model.Segment{}).Where("id = ?", s.ID).Updates(map[string]any{"text": strings.ReplaceAll(s.Text, find, replace), "modified": true, "audio_status": staleIfReady(s.AudioStatus), "prons": model.JSON[[]model.Pronunciation]{V: []model.Pronunciation{}}})
			a.markRenderStale(s.ChapterID)
		}
	}
	return count, matches, nil
}

// BatchPronounce sets the reading of a character (optionally only inside a word) across the book.
func (a *App) BatchPronounce(x *Access, char, word, pinyin string, apply bool) (int, error) {
	if err := need(x, PermScript); err != nil {
		return 0, err
	}
	if utf8.RuneCountInString(char) != 1 {
		return 0, Bad("请选择一个字")
	}
	target := char
	if word != "" {
		target = word
	}
	var segs []model.Segment
	a.DB.Where("book_id = ?", x.Book.ID).Where("script_id IN (?)", a.DB.Model(&model.Chapter{}).Select("current_script_id").Where("book_id = ?", x.Book.ID)).
		Where(dbx.Like(a.DB, "text"), "%"+target+"%").Find(&segs)
	total := 0
	homo := text.Homophone(pinyin)
	for _, s := range segs {
		runes := []rune(s.Text)
		tr := []rune(target)
		charIdx := strings.Index(target, char)
		charOff := utf8.RuneCountInString(target[:charIdx])
		prons := map[int]model.Pronunciation{}
		for _, p := range s.Prons.V {
			prons[p.Offset] = p
		}
		changed := false
		for i := 0; i+len(tr) <= len(runes); i++ {
			if string(runes[i:i+len(tr)]) == target {
				off := i + charOff
				prons[off] = model.Pronunciation{Offset: off, Char: char, Pinyin: pinyin, Homophone: homo}
				changed = true
				total++
			}
		}
		if changed && apply {
			var list []model.Pronunciation
			for _, p := range prons {
				list = append(list, p)
			}
			sort.Slice(list, func(i, j int) bool { return list[i].Offset < list[j].Offset })
			a.DB.Model(&model.Segment{}).Where("id = ?", s.ID).Updates(map[string]any{"prons": model.JSON[[]model.Pronunciation]{V: list}, "audio_status": staleIfReady(s.AudioStatus)})
		}
	}
	return total, nil
}

// IntegrityCheck compares the current script with the chapter source text.
func (a *App) IntegrityCheck(ch *model.Chapter) map[string]any {
	paras, _ := text.Paragraphs(ch.SourceText)
	segs := a.scriptSegments(ch.CurrentScriptID)
	byPara := map[int]string{}
	maxPara := -1
	for _, s := range segs {
		if s.Type == model.SegSfx || s.Type == model.SegPause {
			continue
		}
		byPara[s.Para] += s.Text
		if s.Para > maxPara {
			maxPara = s.Para
		}
	}
	var issues []map[string]any
	for i, p := range paras {
		got := byPara[i]
		if got != p {
			issues = append(issues, map[string]any{"para": i, "expected": p, "actual": got, "similarity": text.Similarity(text.Normalize(p), text.Normalize(got))})
		}
		if len(issues) >= 50 {
			break
		}
	}
	return map[string]any{"ok": len(issues) == 0, "paragraphs": len(paras), "issues": issues}
}

// ---------- versions ----------

func (a *App) ActivateScript(x *Access, ch *model.Chapter, scriptID string) error {
	if err := need(x, PermScript); err != nil {
		return err
	}
	var s model.Script
	if a.DB.First(&s, "id = ? AND chapter_id = ?", scriptID, ch.ID).Error != nil {
		return NotFound("画本版本")
	}
	a.DB.Model(ch).Updates(map[string]any{"current_script_id": s.ID, "render_stale": true})
	a.RecountBook(ch.BookID)
	a.Activity(x.StudioID, ch.BookID, x.User.ID, "script.rollback", "chapter", ch.ID, fmt.Sprintf("将「%s」的画本切换到 v%d", ch.Title, s.Version))
	return nil
}

// CompareScripts diffs two versions by (speaker, text) line identity.
func (a *App) CompareScripts(aID, bID string) []map[string]any {
	left, right := a.scriptSegments(aID), a.scriptSegments(bID)
	key := func(s model.Segment) string { return s.CharacterID + "|" + s.Text }
	var la, lb []string
	for _, s := range left {
		la = append(la, key(s))
	}
	for _, s := range right {
		lb = append(lb, key(s))
	}
	var out []map[string]any
	for _, op := range text.DiffStrings(la, lb) {
		switch op.Kind {
		case "delete":
			out = append(out, map[string]any{"kind": "removed", "text": left[op.A].Text, "characterId": left[op.A].CharacterID})
		case "insert":
			out = append(out, map[string]any{"kind": "added", "text": right[op.B].Text, "characterId": right[op.B].CharacterID})
		}
	}
	return out
}

// ---------- editing lock ----------

const lockTTL = 2 * time.Minute

func (a *App) AcquireLock(x *Access, ch *model.Chapter, force bool) (bool, string) {
	if ch.EditLockUserID != "" && ch.EditLockUserID != x.User.ID && ch.EditLockAt != nil && ch.EditLockAt.After(now().Add(-lockTTL)) && !force {
		var u model.User
		a.DB.Select("nickname").First(&u, "id = ?", ch.EditLockUserID)
		return false, u.Nickname
	}
	t := now()
	a.DB.Model(ch).Updates(map[string]any{"edit_lock_user_id": x.User.ID, "edit_lock_at": &t})
	a.emit(x.StudioID, eventChapter(ch.BookID, ch.ID, "lock"))
	return true, x.User.Nickname
}

func (a *App) ReleaseLock(x *Access, ch *model.Chapter) {
	if ch.EditLockUserID == x.User.ID {
		a.DB.Model(ch).Updates(map[string]any{"edit_lock_user_id": "", "edit_lock_at": nil})
		a.emit(x.StudioID, eventChapter(ch.BookID, ch.ID, "lock"))
	}
}

// SegmentViews returns API views with audio URLs.
func (a *App) SegmentViews(segs []model.Segment) []map[string]any {
	takeIDs := []string{}
	for _, s := range segs {
		if s.SelectedTakeID != "" {
			takeIDs = append(takeIDs, s.SelectedTakeID)
		}
	}
	takes := map[string]model.Take{}
	if len(takeIDs) > 0 {
		var list []model.Take
		a.DB.Where("id IN ?", takeIDs).Find(&list)
		for _, t := range list {
			takes[t.ID] = t
		}
	}
	assetIDs := []string{}
	for _, t := range takes {
		assetIDs = append(assetIDs, t.AssetID)
	}
	for _, s := range segs {
		if s.SfxAssetID != "" {
			assetIDs = append(assetIDs, s.SfxAssetID)
		}
	}
	assets := map[string]*model.Asset{}
	if len(assetIDs) > 0 {
		var list []model.Asset
		a.DB.Where("id IN ?", assetIDs).Find(&list)
		for i := range list {
			assets[list[i].ID] = &list[i]
		}
	}
	// open rework items per line, so pickups are visible right in the script
	type issueRow struct {
		SegmentID string
		N         int
		Verifying int
		Title     string
	}
	issues := map[string]issueRow{}
	if len(segs) > 0 {
		ids := make([]string, 0, len(segs))
		for _, s := range segs {
			ids = append(ids, s.ID)
		}
		for start := 0; start < len(ids); start += 900 {
			end := min(start+900, len(ids))
			var rows []issueRow
			a.DB.Model(&model.Issue{}).Select("segment_id, count(*) AS n, sum(CASE WHEN status = ? THEN 1 ELSE 0 END) AS verifying, max(title) AS title", model.IssueVerifying).
				Where("segment_id IN ? AND status <> ?", ids[start:end], model.IssueClosed).Group("segment_id").Scan(&rows)
			for _, r := range rows {
				issues[r.SegmentID] = r
			}
		}
	}
	out := make([]map[string]any, 0, len(segs))
	for _, s := range segs {
		v := map[string]any{
			"id": s.ID, "scriptId": s.ScriptID, "chapterId": s.ChapterID, "seq": s.Seq, "para": s.Para, "sceneBreak": s.SceneBreak,
			"type": s.Type, "characterId": s.CharacterID, "text": s.Text, "removed": s.Removed, "emotion": s.Emotion, "intensity": s.Intensity,
			"tags": s.Tags.V, "speed": s.Speed, "volume": s.Volume, "pitch": s.Pitch, "pauseBeforeMs": s.PauseBeforeMs, "pauseAfterMs": s.PauseAfterMs,
			"instruction": s.Instruction, "prons": s.Prons.V, "cues": nonNilCues(s.Cues.V), "effects": s.Effects.V, "sfxAssetId": s.SfxAssetID, "confidence": s.Confidence,
			"needsReview": s.NeedsReview, "locked": s.Locked, "selectedTakeId": s.SelectedTakeID, "audioStatus": s.AudioStatus,
			"recordStatus": s.RecordStatus, "qc": s.QC, "qcNote": s.QCNote, "lastError": s.LastError, "note": s.Note, "modified": s.Modified,
			"freeRegens": s.FreeRegens,
		}
		if r, ok := issues[s.ID]; ok {
			v["openIssues"], v["verifyingIssues"], v["issueTitle"] = r.N, r.Verifying, r.Title
		}
		if t, ok := takes[s.SelectedTakeID]; ok {
			v["take"] = map[string]any{"id": t.ID, "origin": t.Origin, "durationMs": t.DurationMs, "url": a.AssetURL(assets[t.AssetID]), "createdAt": t.CreatedAt}
		}
		if as := assets[s.SfxAssetID]; as != nil {
			v["sfx"] = map[string]any{"id": as.ID, "name": as.Name, "url": a.AssetURL(as), "durationMs": as.DurationMs}
		}
		out = append(out, v)
	}
	return out
}
