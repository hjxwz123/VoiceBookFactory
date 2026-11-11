package core

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"gorm.io/gorm"

	"voicebook/internal/jobs"
	"voicebook/internal/model"
)

func (a *App) EnqueueCharacterAnalysis(x *Access, bookID string) (*model.Job, error) {
	var running int64
	a.DB.Model(&model.Job{}).Where("book_id = ? AND type = ? AND status IN ?", bookID, "character_analysis", []string{model.JobQueued, model.JobRunning}).Count(&running)
	if running > 0 {
		return nil, Conflict("角色分析正在进行中")
	}
	if _, err := a.LLMFor(FeatCharacter, "", x.StudioID); err != nil {
		return nil, err
	}
	j := &model.Job{Type: "character_analysis", StudioID: x.StudioID, BookID: bookID, RefType: "book", RefID: bookID, Title: "角色分析", CreatedBy: x.User.ID, MaxAttempts: 2}
	return j, a.Q.Enqueue(j)
}

func (a *App) jobCharacterAnalysis(ctx context.Context, jc *jobs.Context) error {
	j := jc.Job
	var b model.Book
	if err := a.DB.First(&b, "id = ?", j.BookID).Error; err != nil {
		return jobs.Permanent(err)
	}
	var chapters []model.Chapter
	q := a.DB.Where("book_id = ? AND included = ?", b.ID, true).Order("\"index\" asc")
	if ids, ok := j.Payload.V["chapterIds"].([]any); ok && len(ids) > 0 {
		q = q.Where("id IN ?", ids)
	}
	q.Find(&chapters)
	if len(chapters) == 0 {
		return jobs.Permanent(fmt.Errorf("没有可分析的正文章节"))
	}
	h, err := a.LLMFor(FeatCharacter, b.Settings.V.QualityTier, b.StudioID)
	if err != nil {
		return jobs.Permanent(err)
	}
	est := a.Estimate(FeatCharacter, "", float64(min(b.WordCount, 72000)))
	if err := a.Hold(j.ID, b.StudioID, est); err != nil {
		return jobs.Permanent(err)
	}
	infos, chars, err := a.AnalyzeCharacters(ctx, h, chapters, func(p float64, t string) { jc.Progress(p*0.9, t) })
	if err != nil {
		return err
	}
	if _, err := a.ChargeCredits(Charge{StudioID: b.StudioID, UserID: j.CreatedBy, BookID: b.ID, JobID: j.ID, Feature: FeatCharacter, Quantity: float64(chars), Unit: "chars", AccountID: h.Account.ID, Provider: h.Account.Provider, Model: h.Client.Model(), BYOK: h.BYOK}); err != nil {
		return jobs.Permanent(err)
	}
	created, updated := a.SaveCharacterInfos(&b, infos)
	jc.SetResult(map[string]any{"found": len(infos), "created": created, "updated": updated})
	jc.Progress(1, fmt.Sprintf("识别出 %d 个角色（新增 %d，更新 %d）", len(infos), created, updated))
	a.Activity(b.StudioID, b.ID, j.CreatedBy, "character.analysis", "book", b.ID, fmt.Sprintf("AI 角色分析识别出 %d 个角色", len(infos)))
	a.emit(b.StudioID, eventBook(b.ID, "characters"))
	return nil
}

// SaveCharacterInfos merges analysis results into the book's characters without overwriting user edits.
func (a *App) SaveCharacterInfos(b *model.Book, infos []CharacterInfo) (int, int) {
	var existing []model.Character
	a.DB.Where("book_id = ?", b.ID).Find(&existing)
	match := func(ci CharacterInfo) *model.Character {
		names := append([]string{ci.Name}, ci.Aliases...)
		for i := range existing {
			e := &existing[i]
			all := append([]string{e.Name}, e.Aliases.V...)
			for _, n := range names {
				if contains(all, n) {
					return e
				}
			}
		}
		return nil
	}
	created, updated := 0, 0
	for _, ci := range infos {
		if e := match(ci); e != nil {
			up := map[string]any{}
			aliases := e.Aliases.V
			for _, al := range append(ci.Aliases, ci.Name) {
				if al != e.Name && !contains(aliases, al) {
					aliases = append(aliases, al)
				}
			}
			up["aliases"] = model.Strings(aliases...)
			setIfEmpty := func(col, cur, val string) {
				if (cur == "" || cur == "unknown") && val != "" && val != "unknown" {
					up[col] = val
				}
			}
			setIfEmpty("gender", e.Gender, ci.Gender)
			setIfEmpty("age_group", e.AgeGroup, ci.AgeGroup)
			setIfEmpty("identity", e.Identity, ci.Identity)
			setIfEmpty("personality", e.Personality, ci.Personality)
			setIfEmpty("voice_traits", e.VoiceTraits, ci.VoiceTraits)
			setIfEmpty("profile", e.Profile, ci.Profile)
			if e.Candidate {
				up["candidate"] = false
				up["kind"] = ci.Kind
			}
			a.DB.Model(e).Updates(up)
			updated++
			continue
		}
		c := model.Character{
			BookID: b.ID, Name: truncateStr(ci.Name, 60), Aliases: model.Strings(ci.Aliases...), Kind: firstNonEmpty(ci.Kind, model.CharMinor),
			Gender: firstNonEmpty(ci.Gender, "unknown"), AgeGroup: firstNonEmpty(ci.AgeGroup, "adult"), Identity: truncateStr(ci.Identity, 190),
			Personality: truncateStr(ci.Personality, 490), VoiceTraits: truncateStr(ci.VoiceTraits, 490), Profile: ci.Profile, Mode: "ai",
			Color: charColors[(len(existing)+created)%len(charColors)], SortOrder: len(existing) + created, UseCrowdPool: ci.Kind == model.CharCrowd,
		}
		c.Params.V = model.VoiceParams{}
		c.Casting.V = []model.CastingCandidate{}
		if a.DB.Create(&c).Error == nil {
			created++
			existing = append(existing, c)
		}
	}
	return created, updated
}

// MergeCharacters moves all lines of `from` into `into` and deletes `from`.
func (a *App) MergeCharacters(x *Access, intoID, fromID string) error {
	if err := need(x, PermScript); err != nil {
		return err
	}
	if intoID == fromID {
		return Bad("不能与自身合并")
	}
	var into, from model.Character
	if a.DB.First(&into, "id = ? AND book_id = ?", intoID, x.Book.ID).Error != nil || a.DB.First(&from, "id = ? AND book_id = ?", fromID, x.Book.ID).Error != nil {
		return NotFound("角色")
	}
	if from.Kind == model.CharNarrator && into.Kind != model.CharNarrator {
		return Bad("旁白不能合并到其他角色")
	}
	return a.DB.Transaction(func(tx *gorm.DB) error {
		aliases := into.Aliases.V
		for _, n := range append(from.Aliases.V, from.Name) {
			if n != into.Name && !contains(aliases, n) {
				aliases = append(aliases, n)
			}
		}
		tx.Model(&into).Updates(map[string]any{"aliases": model.Strings(aliases...), "candidate": false})
		// lines change voice: their audio becomes stale
		tx.Model(&model.Segment{}).Where("character_id = ?", from.ID).Updates(map[string]any{"character_id": into.ID, "audio_status": gorm.Expr("CASE WHEN audio_status = ? THEN ? ELSE audio_status END", model.AudioReady, model.AudioStale)})
		tx.Model(&model.ProductionTask{}).Where("character_id = ?", from.ID).Update("character_id", into.ID)
		return tx.Delete(&from).Error
	})
}

// ---------- casting ----------

// CastingPool returns voices usable by a studio (official, studio, own personal, licensed market voices).
func (a *App) CastingPool(studioID, userID, language string) []model.Voice {
	var voices []model.Voice
	q := a.DB.Where("status = ?", model.VoiceReady).Where(
		a.DB.Where("scope = ? AND listed = ?", model.ScopeSystem, true).
			Or("scope = ? AND studio_id = ?", model.ScopeStudio, studioID).
			Or("scope = ? AND owner_user_id = ?", model.ScopeUser, userID).
			Or("scope = ? AND studio_id = ? AND shared = ?", model.ScopeUser, studioID, true)).
		Where("(scope <> ? OR provider IN ?)", model.ScopeSystem, a.ActiveTTSProviders())
	if !a.StudioPlan(studioID).HighQuality { // premium-tier platform voices are a membership benefit (own clones always count)
		q = q.Where("(scope <> ? OR price_tier <> ?)", model.ScopeSystem, model.TierPremium)
	}
	q.Order("CASE WHEN provider = 'placeholder' THEN 1 ELSE 0 END, recommended desc, sort_order asc").Find(&voices)
	if language != "" {
		var filtered []model.Voice
		for _, v := range voices {
			if languageFits(language, v.Language) {
				filtered = append(filtered, v)
			}
		}
		if len(filtered) > 0 {
			voices = filtered
		}
	}
	return voices
}

// languageFits reports whether a voice can read a book: same language family, and dialect / Cantonese voices
// only for books written in that dialect.
func languageFits(book, voice string) bool {
	if voice == "" || voice == book {
		return true
	}
	special := func(l string) bool { return l == "zh-HK" || l == "zh-dialect" }
	if special(voice) || special(book) {
		return false
	}
	return strings.Split(voice, "-")[0] == strings.Split(book, "-")[0]
}

// Cast computes casting suggestions (LLM ranked, rule fallback) and optionally applies the top pick.
func (a *App) Cast(ctx context.Context, x *Access, characterIDs []string, apply bool, jc *jobs.Context) (map[string][]model.CastingCandidate, error) {
	if err := need(x, PermCasting); err != nil {
		return nil, err
	}
	var chars []model.Character
	q := a.DB.Where("book_id = ? AND candidate = ?", x.Book.ID, false)
	if len(characterIDs) > 0 {
		q = q.Where("id IN ?", characterIDs)
	}
	q.Order("sort_order asc").Find(&chars)
	if len(chars) == 0 {
		return nil, Bad("没有需要选角的角色")
	}
	pool := a.CastingPool(x.StudioID, x.User.ID, x.Book.Language)
	if len(pool) == 0 {
		return nil, Bad("音色库中没有可用的音色")
	}
	tier := x.Book.Settings.V.QualityTier
	policy := a.CastingPolicy(tier)
	prefer := func(kind string) string {
		switch kind {
		case model.CharNarrator:
			return policy.Narrator
		case model.CharLead:
			return policy.Lead
		case model.CharSupporting:
			return policy.Supporting
		case model.CharCrowd:
			return policy.Crowd
		}
		return policy.Minor
	}
	result := map[string][]model.CastingCandidate{}
	usedLLM := false
	if h, err := a.LLMFor(FeatCasting, tier, x.StudioID); err == nil {
		// keep the prompt compact: a rule-ranked shortlist per character (the library may hold thousands of voices)
		compact := CastingShortlist(chars, pool, prefer, 10, 90)
		if jc != nil {
			jc.Progress(0.2, "AI 正在为角色挑选音色")
		}
		res, n, err := a.RecommendVoices(ctx, h, chars, compact)
		if err == nil {
			usedLLM = true
			result = res
			a.ChargeCredits(Charge{StudioID: x.StudioID, UserID: x.User.ID, BookID: x.Book.ID, Feature: FeatCasting, Quantity: float64(n), Unit: "chars", AccountID: h.Account.ID, Provider: h.Account.Provider, Model: h.Client.Model(), BYOK: h.BYOK})
		}
	}
	for _, c := range chars {
		if len(result[c.ID]) == 0 {
			result[c.ID] = HeuristicCasting(c, pool, prefer(c.Kind))
		}
	}
	// avoid giving two important characters the same top voice
	taken := map[string]string{}
	sort.SliceStable(chars, func(i, j int) bool { return kindRank[chars[i].Kind] > kindRank[chars[j].Kind] })
	for _, c := range chars {
		cands := result[c.ID]
		a.DB.Model(&model.Character{}).Where("id = ?", c.ID).Update("casting", model.JSON[[]model.CastingCandidate]{V: cands})
		if !apply || c.VoiceID != "" || c.Mode == "human" || len(cands) == 0 {
			continue
		}
		pick := cands[0].VoiceID
		if c.Kind != model.CharCrowd && c.Kind != model.CharMinor {
			for _, cand := range cands {
				if _, used := taken[cand.VoiceID]; !used {
					pick = cand.VoiceID
					break
				}
			}
			taken[pick] = c.ID
		}
		a.DB.Model(&model.Character{}).Where("id = ?", c.ID).Update("voice_id", pick)
	}
	how := "规则匹配"
	if usedLLM {
		how = "AI"
	}
	a.Activity(x.StudioID, x.Book.ID, x.User.ID, "casting", "book", x.Book.ID, fmt.Sprintf("%s 选角：为 %d 个角色生成候选音色", how, len(chars)))
	a.emit(x.StudioID, eventBook(x.Book.ID, "characters"))
	return result, nil
}

// SetCharacterVoice binds a voice and marks that character's generated lines as stale.
func (a *App) SetCharacterVoice(x *Access, c *model.Character, voiceID string) (int64, error) {
	if err := need(x, PermCasting); err != nil {
		return 0, err
	}
	if voiceID != "" {
		var v model.Voice
		if a.DB.First(&v, "id = ?", voiceID).Error != nil || v.Status != model.VoiceReady {
			return 0, Bad("音色不可用")
		}
		if err := a.checkPremiumVoice(x.StudioID, &v); err != nil {
			return 0, err
		}
	}
	if c.VoiceID == voiceID {
		return 0, nil
	}
	if err := a.DB.Model(c).Updates(map[string]any{"voice_id": voiceID, "mode": "ai"}).Error; err != nil {
		return 0, err
	}
	res := a.DB.Model(&model.Segment{}).Where("character_id = ? AND audio_status = ? AND locked = ?", c.ID, model.AudioReady, false).
		Where("selected_take_id IN (?)", a.DB.Model(&model.Take{}).Select("id").Where("origin = ?", model.TakeTTS)).
		Update("audio_status", model.AudioStale)
	a.DB.Model(&model.Chapter{}).Where("book_id = ? AND render_id <> ''", c.BookID).Update("render_stale", true)
	a.Activity(x.StudioID, c.BookID, x.User.ID, "casting.set", "character", c.ID, fmt.Sprintf("为「%s」更换了音色", c.Name))
	return res.RowsAffected, nil
}

// AffectedByRecast counts generated lines that a voice change would invalidate (for the confirm dialog).
func (a *App) AffectedByRecast(c *model.Character) (int64, int64) {
	var n int64
	a.DB.Model(&model.Segment{}).Where("character_id = ? AND audio_status = ?", c.ID, model.AudioReady).Count(&n)
	var chars int64
	a.DB.Model(&model.Segment{}).Where("character_id = ?", c.ID).Select("COALESCE(SUM(LENGTH(text)),0)").Scan(&chars)
	return n, chars
}

// AuditionLines picks representative lines of a character (varied emotions).
func (a *App) AuditionLines(c *model.Character, n int) []string {
	var segs []model.Segment
	a.DB.Joins("JOIN chapters ON chapters.current_script_id = segments.script_id").
		Where("segments.character_id = ? AND segments.removed = ? AND LENGTH(segments.text) BETWEEN 8 AND 120", c.ID, false).
		Order("segments.seq asc").Limit(200).Find(&segs)
	seen := map[string]bool{}
	var out []string
	for _, s := range segs {
		if len(out) >= n {
			break
		}
		if !seen[s.Emotion] {
			seen[s.Emotion] = true
			out = append(out, s.Text)
		}
	}
	for _, s := range segs {
		if len(out) >= n {
			break
		}
		if !contains(out, s.Text) {
			out = append(out, s.Text)
		}
	}
	if len(out) == 0 {
		if c.Kind == model.CharNarrator {
			out = []string{"夜色渐深，城市的灯火一盏盏亮起。她站在窗前，望着远处的江面，心里有一种说不出的平静。"}
		} else {
			out = []string{fmt.Sprintf("你好，我是%s。很高兴认识你。", c.Name), "这件事，我们得好好谈一谈。"}
		}
	}
	return out
}
