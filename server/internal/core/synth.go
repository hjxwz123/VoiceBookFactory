package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"gorm.io/gorm"

	"voicebook/internal/ai/tts"
	"voicebook/internal/align"
	"voicebook/internal/audio"
	"voicebook/internal/jobs"
	"voicebook/internal/model"
	"voicebook/internal/text"
)

// lexicon returns active entries visible for a book (book > studio > platform).
func (a *App) lexicon(studioID, bookID string) []model.LexiconEntry {
	var list []model.LexiconEntry
	a.DB.Where("status = ? AND ((scope = 'platform') OR (scope = 'studio' AND studio_id = ?) OR (scope = 'book' AND book_id = ?))", "active", studioID, bookID).Find(&list)
	rank := map[string]int{"book": 0, "studio": 1, "platform": 2}
	sort.SliceStable(list, func(i, j int) bool {
		if rank[list[i].Scope] != rank[list[j].Scope] {
			return rank[list[i].Scope] < rank[list[j].Scope]
		}
		return utf8.RuneCountInString(list[i].Term) > utf8.RuneCountInString(list[j].Term)
	})
	return list
}

// BuildTTSInput applies segment pronunciations and the lexicon. Providers with phoneme support get
// structured readings; others receive homophone substitutions.
func BuildTTSInput(txt string, prons []model.Pronunciation, lex []model.LexiconEntry, phoneme bool) (string, []tts.Pron) {
	out, structured, _ := BuildTTSInputMapped(txt, prons, lex, phoneme)
	return out, structured
}

// BuildTTSInputMapped also returns where each input rune ended up in the output (posMap[len(input)] = output length),
// so positional cues survive lexicon replacements.
func BuildTTSInputMapped(txt string, prons []model.Pronunciation, lex []model.LexiconEntry, phoneme bool) (string, []tts.Pron, []int) {
	runes := []rune(txt)
	readings := map[int]model.Pronunciation{}
	for _, p := range prons {
		if p.Offset >= 0 && p.Offset < len(runes) {
			readings[p.Offset] = p
		}
	}
	replacements := map[int][2]any{} // offset -> {length, replacement}
	for _, e := range lex {
		term := []rune(e.Term)
		if len(term) == 0 {
			continue
		}
		pys := strings.Fields(e.Pinyin)
		for i := 0; i+len(term) <= len(runes); i++ {
			if string(runes[i:i+len(term)]) != e.Term {
				continue
			}
			if e.Replacement != "" {
				replacements[i] = [2]any{len(term), e.Replacement}
				continue
			}
			if len(pys) == len(term) {
				for k := range term {
					// any character with more than one reading, including rare ones used in names (嬛, 瑱 …)
					if _, set := readings[i+k]; !set && (text.IsPolyphonic(term[k]) || len(text.Readings(string(term[k]))) > 1) {
						readings[i+k] = model.Pronunciation{Offset: i + k, Char: string(term[k]), Pinyin: pys[k], Homophone: text.Homophone(pys[k])}
					}
				}
			}
		}
	}
	var out strings.Builder
	var structured []tts.Pron
	posMap := make([]int, len(runes)+1)
	outLen := 0
	for i := 0; i < len(runes); i++ {
		posMap[i] = outLen
		if r, ok := replacements[i]; ok {
			out.WriteString(r[1].(string))
			for k := 1; k < r[0].(int) && i+k < len(runes); k++ {
				posMap[i+k] = outLen
			}
			outLen += utf8.RuneCountInString(r[1].(string))
			i += r[0].(int) - 1
			continue
		}
		if p, ok := readings[i]; ok {
			if phoneme {
				structured = append(structured, tts.Pron{Offset: utf8.RuneCountInString(out.String()), Char: p.Char, Pinyin: p.Pinyin, Homophone: p.Homophone})
			} else if p.Homophone != "" {
				out.WriteString(p.Homophone)
				outLen += utf8.RuneCountInString(p.Homophone)
				continue
			}
		}
		out.WriteRune(runes[i])
		outLen++
	}
	posMap[len(runes)] = outLen
	return out.String(), structured, posMap
}

// mapCues moves cue offsets through a BuildTTSInputMapped position map.
func mapCues(cs []model.Cue, posMap []int) []tts.Cue {
	out := make([]tts.Cue, 0, len(cs))
	for _, c := range cs {
		at := c.At
		if at < 0 {
			at = 0
		}
		if at >= len(posMap) {
			at = len(posMap) - 1
		}
		out = append(out, tts.Cue{At: posMap[at], Tag: c.Tag})
	}
	return out
}

type voiceChoice struct {
	Voice  *model.Voice
	Params model.VoiceParams
	Crowd  bool
}

// resolveVoice finds the voice for a segment's character (crowd pool rotation for extras).
func (a *App) resolveVoice(c *model.Character, studioID string) (*voiceChoice, error) {
	if c == nil {
		return nil, Bad("片段没有指定角色")
	}
	if c.VoiceID != "" {
		var v model.Voice
		if err := a.DB.First(&v, "id = ?", c.VoiceID).Error; err != nil {
			return nil, Bad("角色「%s」绑定的音色已被删除，请重新选角", c.Name)
		}
		if v.Status != model.VoiceReady {
			return nil, Bad("角色「%s」的音色「%s」当前不可用（%s）", c.Name, v.Name, v.Status)
		}
		return &voiceChoice{Voice: &v, Params: c.Params.V}, nil
	}
	if c.UseCrowdPool || c.Kind == model.CharCrowd {
		var pool []model.Voice
		q := a.DB.Where("crowd_pool = ? AND status = ? AND (scope = 'system' OR studio_id = ?)", true, model.VoiceReady, studioID).
			Where("(scope <> ? OR provider IN ?)", model.ScopeSystem, a.ActiveTTSProviders())
		if c.Gender == "male" || c.Gender == "female" {
			q = q.Where("gender = ?", c.Gender)
		}
		q.Order("sort_order asc").Find(&pool)
		if len(pool) > 0 {
			idx := int(audio.Seed(c.ID) % int64(len(pool)))
			return &voiceChoice{Voice: &pool[idx], Params: c.Params.V, Crowd: true}, nil
		}
	}
	return nil, Bad("角色「%s」还没有选择音色", c.Name)
}

// segmentVoice is the voice a line is spoken in: its own voice when one was set for it in the
// script editor, otherwise its character's.
func (a *App) segmentVoice(s *model.Segment, c *model.Character, studioID string) (*voiceChoice, error) {
	if s.VoiceID != "" {
		var v model.Voice
		if err := a.DB.First(&v, "id = ?", s.VoiceID).Error; err != nil {
			return nil, Bad("这句单独设置的音色已被删除，请在制作台重新选择")
		}
		if v.Status != model.VoiceReady {
			return nil, Bad("这句单独设置的音色「%s」当前不可用", v.Name)
		}
		return &voiceChoice{Voice: &v}, nil
	}
	return a.resolveVoice(c, studioID)
}

func segHash(s *model.Segment, vc *voiceChoice) string {
	prons, _ := json.Marshal(s.Prons.V)
	speed := f64(s.Speed, f64(vc.Params.Speed, vc.Voice.Defaults.V.Speed))
	pitch := f64(s.Pitch, f64(vc.Params.Pitch, vc.Voice.Defaults.V.Pitch))
	cues, _ := json.Marshal(s.Cues.V)
	return hashParts(s.Text, vc.Voice.ID, s.Emotion, fmt.Sprint(s.Intensity), strings.Join(s.Tags.V, ","), fmt.Sprintf("%.3f|%.3f", speed, pitch), s.Instruction, string(prons), string(cues))
}

type synthOutcome struct {
	Take    *model.Take
	Credits int64
	Cached  bool
}

// synthesizeSegment generates (or reuses) audio for one segment and selects it.
func (a *App) synthesizeSegment(ctx context.Context, b *model.Book, s *model.Segment, chars map[string]*model.Character, lex []model.LexiconEntry, userID, jobID string, regenerate bool) (*synthOutcome, error) {
	c := chars[s.CharacterID]
	if c == nil {
		return nil, Bad("片段未指定角色，请先在画本中选择说话人")
	}
	vc, err := a.segmentVoice(s, c, b.StudioID)
	if err != nil {
		return nil, err
	}
	if err := a.checkPremiumVoice(b.StudioID, vc.Voice); err != nil {
		return nil, err
	}
	provider, acc, err := a.TTSFor(vc.Voice)
	if err != nil {
		return nil, err
	}
	caps := tts.CapsFor(provider, vc.Voice.Model, vc.Voice.ProviderVoiceID)
	ttsText, prons, posMap := BuildTTSInputMapped(s.Text, s.Prons.V, lex, caps.Phoneme)
	var cues []tts.Cue
	if caps.InlineTags || caps.Instruction {
		cues = mapCues(s.Cues.V, posMap)
	}
	if strings.TrimSpace(ttsText) == "" {
		return nil, Bad("片段内容为空")
	}
	defaults := vc.Voice.Defaults.V
	speed := f64(s.Speed, f64(vc.Params.Speed, defaults.Speed))
	if speed == 0 {
		speed = 1
	}
	pitch := f64(s.Pitch, f64(vc.Params.Pitch, defaults.Pitch))
	emotion := s.Emotion
	if emotion == "" || emotion == "neutral" {
		emotion = firstNonEmpty(vc.Params.Emotion, defaults.Emotion, "neutral")
	}
	if caps.Emotion && len(caps.Emotions) > 0 && !contains(caps.Emotions, emotion) {
		emotion = "neutral"
	}
	instruction := s.Instruction
	if !caps.Instruction {
		instruction = ""
	}
	tags := s.Tags.V
	if !caps.InlineTags {
		tags = nil
	}
	hash := segHash(s, vc)
	cacheKey := hashParts(acc.ID, vc.Voice.ProviderVoiceID, vc.Voice.Model, ttsText, emotion, fmt.Sprint(s.Intensity), fmt.Sprintf("%.3f|%.3f", speed, pitch), instruction, strings.Join(tags, ","), fmt.Sprint(prons), fmt.Sprint(cues))
	// cache: identical request already synthesized anywhere -> reuse without charge
	if !regenerate {
		var cached model.Take
		if a.DB.Where("cache_key = ? AND status = ? AND origin = ?", cacheKey, model.AudioReady, model.TakeTTS).Order("created_at desc").First(&cached).Error == nil && a.Store.Exists(a.assetKeyByID(cached.AssetID)) {
			t := model.Take{SegmentID: s.ID, ChapterID: s.ChapterID, Origin: model.TakeTTS, VoiceID: vc.Voice.ID, Provider: acc.Provider, Model: vc.Voice.Model, CacheKey: cacheKey, ContentHash: hash, AssetID: cached.AssetID, DurationMs: cached.DurationMs, Status: model.AudioReady, CreatedBy: userID, Params: model.EmptyMap(), QCDetail: model.EmptyMap(), Note: "缓存复用"}
			if err := a.DB.Create(&t).Error; err != nil {
				return nil, err
			}
			a.selectTake(s, &t, hash)
			a.ChargeCredits(Charge{StudioID: b.StudioID, UserID: userID, BookID: b.ID, ChapterID: s.ChapterID, JobID: jobID, Feature: FeatTTS, Tier: vc.Voice.PriceTier, Quantity: float64(utf8.RuneCountInString(ttsText)), Unit: "chars", Provider: acc.Provider, AccountID: acc.ID, Free: true, Note: "缓存复用"})
			return &synthOutcome{Take: &t, Cached: true}, nil
		}
	}
	free := false
	if regenerate && s.ContentHash == hash && s.FreeRegens < 2 {
		free = true
	}
	audioOut, err := provider.Synthesize(ctx, tts.SynthesizeRequest{
		Text: ttsText, ProviderVoiceID: vc.Voice.ProviderVoiceID, Model: vc.Voice.Model, Language: b.Language, Emotion: emotion, Intensity: s.Intensity,
		Speed: speed, Pitch: pitch, Instruction: instruction, Tags: tags, Cues: cues, Prons: prons, SampleRate: audio.SampleRate, Seed: regenSeed(regenerate),
		VoiceGender: vc.Voice.Gender, VoiceAge: vc.Voice.AgeGroup, VoiceSeed: vc.Voice.ID,
	})
	if err != nil {
		a.DB.Model(acc).Update("failures", gorm.Expr("failures + 1"))
		return nil, withDetail("配音失败", fmt.Errorf("账号「%s」：%w", acc.Name, err))
	}
	a.voiceUsed(vc.Voice, provider)
	asset, dur, err := a.storeSpeech(ctx, b.StudioID, userID, audioOut, "take", map[string]any{"segmentId": s.ID})
	if err != nil {
		return nil, err
	}
	// licensed market voice surcharge
	var extra int64
	var listing model.VoiceListing
	nChars := float64(utf8.RuneCountInString(ttsText))
	if a.DB.First(&listing, "voice_id = ? AND status = ?", vc.Voice.ID, "listed").Error == nil && listing.PricePer1K > 0 {
		var owner model.User
		if a.DB.First(&owner, "id = ?", listing.OwnerUserID).Error == nil && owner.PersonalStudio != b.StudioID {
			extra = int64(nChars/1000*float64(listing.PricePer1K) + 0.999)
		}
	}
	credits, err := a.ChargeCredits(Charge{StudioID: b.StudioID, UserID: userID, BookID: b.ID, ChapterID: s.ChapterID, JobID: jobID, Feature: FeatTTS, Tier: vc.Voice.PriceTier, Quantity: nChars, Unit: "chars", Provider: acc.Provider, AccountID: acc.ID, Model: vc.Voice.Model, BYOK: acc.Scope == "studio", CostCNY: nChars / 1000 * acc.CostPerUnit, Free: free, Extra: extra})
	if err != nil {
		return nil, err
	}
	if extra > 0 {
		a.payVoiceOwner(&listing, extra)
	}
	t := model.Take{SegmentID: s.ID, ChapterID: s.ChapterID, Origin: model.TakeTTS, VoiceID: vc.Voice.ID, Provider: acc.Provider, Model: vc.Voice.Model, CacheKey: cacheKey, ContentHash: hash,
		AssetID: asset.ID, DurationMs: dur, Status: model.AudioReady, CreatedBy: userID, Credits: credits,
		Params: model.Map{V: map[string]any{"emotion": emotion, "speed": speed, "pitch": pitch, "crowd": vc.Crowd}}, QCDetail: model.EmptyMap()}
	if err := a.DB.Create(&t).Error; err != nil {
		return nil, err
	}
	if free {
		a.DB.Model(s).Update("free_regens", s.FreeRegens+1)
	}
	a.selectTake(s, &t, hash)
	a.DB.Model(vc.Voice).Update("usage_count", gorm.Expr("usage_count + 1"))
	return &synthOutcome{Take: &t, Credits: credits}, nil
}

func (a *App) assetKeyByID(id string) string {
	var as model.Asset
	if a.DB.Select("storage_key").First(&as, "id = ?", id).Error != nil {
		return ""
	}
	return as.StorageKey
}

// storeSpeech normalizes provider audio (decode, trim, level) and stores it as a WAV asset.
func (a *App) storeSpeech(ctx context.Context, studioID, userID string, au *tts.Audio, kind string, meta map[string]any) (*model.Asset, int, error) {
	var pcm *audio.PCM
	var err error
	if au.Format == "wav" {
		pcm, err = audio.DecodeWAV(bytes.NewReader(au.Data))
	}
	if pcm == nil || err != nil {
		dir, derr := a.Store.TempDir("tts")
		if derr != nil {
			return nil, 0, derr
		}
		defer os.RemoveAll(dir)
		in := filepath.Join(dir, "in."+firstNonEmpty(au.Format, "mp3"))
		os.WriteFile(in, au.Data, 0o644)
		pcm, err = a.FF.DecodePCM(ctx, in, audio.SampleRate, 1, "")
		if err != nil {
			return nil, 0, err
		}
	}
	pcm = audio.Resample(audio.ToMono(pcm), audio.SampleRate)
	pcm = audio.TrimSilence(pcm, -48, 40)
	audio.NormalizeRMS(pcm, -20, -2)
	var buf bytes.Buffer
	if err := audio.EncodeWAV16(&buf, pcm); err != nil {
		return nil, 0, err
	}
	asset, err := a.SaveAssetBytes(ctx, AssetInput{Scope: model.ScopeStudio, StudioID: studioID, Kind: kind, Name: kind, Ext: "wav", Mime: "audio/wav", Origin: "generated", CreatedBy: userID, Meta: meta, NoProbe: true}, buf.Bytes())
	if err != nil {
		return nil, 0, err
	}
	dur := int(pcm.DurationMs())
	a.DB.Model(asset).Updates(map[string]any{"duration_ms": dur, "sample_rate": pcm.Rate, "channels": 1})
	peaks, _ := json.Marshal(audio.Peaks(pcm, 50))
	key := "peaks/" + asset.ID + ".json"
	if _, err := a.Store.PutBytes(key, peaks); err == nil {
		a.DB.Model(asset).Update("peaks_key", key)
	}
	return asset, dur, nil
}

func (a *App) selectTake(s *model.Segment, t *model.Take, hash string) {
	up := map[string]any{"selected_take_id": t.ID, "audio_status": model.AudioReady, "content_hash": hash, "last_error": "", "qc": t.QC, "qc_note": ""}
	if t.Origin != model.TakeTTS {
		up["record_status"] = model.RecRecorded
	}
	a.DB.Model(&model.Segment{}).Where("id = ?", s.ID).Updates(up)
	s.SelectedTakeID, s.AudioStatus, s.ContentHash = t.ID, model.AudioReady, hash
	a.DB.Model(&model.Chapter{}).Where("id = ?", s.ChapterID).Update("render_stale", true)
	// link the new take to an open issue on this segment
	a.DB.Model(&model.Issue{}).Where("segment_id = ? AND status IN ?", s.ID, []string{model.IssueOpen, model.IssueInProgress}).
		Updates(map[string]any{"new_take_id": t.ID, "status": model.IssueVerifying})
}

// SelectTake lets a user choose among a segment's takes.
func (a *App) SelectTake(x *Access, s *model.Segment, takeID string) error {
	if !x.Can(PermGenerate) && !x.Can(PermRecord) && !x.Can(PermPost) {
		return Forbidden("")
	}
	var t model.Take
	if a.DB.First(&t, "id = ? AND segment_id = ?", takeID, s.ID).Error != nil {
		return NotFound("音频版本")
	}
	hash := t.ContentHash
	if t.Origin != model.TakeTTS {
		hash = s.ContentHash
	}
	a.selectTake(s, &t, hash)
	return nil
}

func (a *App) payVoiceOwner(l *model.VoiceListing, credits int64) {
	share := a.Billing().MarketShare
	income := int64(float64(credits) * share)
	var owner model.User
	if income <= 0 || a.DB.First(&owner, "id = ?", l.OwnerUserID).Error != nil {
		return
	}
	a.Grant(owner.PersonalStudio, income, "market", nil, "授权音色收益", "")
	a.DB.Model(l).Updates(map[string]any{"uses": gorm.Expr("uses + 1"), "earned": gorm.Expr("earned + ?", income)})
}

// ---------- generation jobs ----------

type GenerateRequest struct {
	Scope        string   `json:"scope"` // all | missing | stale | characters | segments | failed
	CharacterIDs []string `json:"characterIds"`
	SegmentIDs   []string `json:"segmentIds"`
	Regenerate   bool     `json:"regenerate"`
	AutoRender   bool     `json:"autoRender"`
}

func (a *App) generationTargets(ch *model.Chapter, req GenerateRequest, x *Access) ([]model.Segment, error) {
	if ch.CurrentScriptID == "" {
		return nil, Bad("「%s」还没有画本，请先生成画本", ch.Title)
	}
	q := a.DB.Where("script_id = ? AND removed = ? AND type NOT IN ?", ch.CurrentScriptID, false, []string{model.SegSfx, model.SegPause})
	switch req.Scope {
	case "segments":
		q = q.Where("id IN ?", req.SegmentIDs)
	case "characters":
		q = q.Where("character_id IN ?", req.CharacterIDs).Where("audio_status <> ?", model.AudioReady)
	case "stale":
		q = q.Where("audio_status = ?", model.AudioStale)
	case "failed":
		q = q.Where("audio_status = ?", model.AudioFailed)
	case "all":
		if !req.Regenerate {
			q = q.Where("audio_status <> ?", model.AudioReady)
		}
	default:
		q = q.Where("audio_status <> ?", model.AudioReady)
	}
	var segs []model.Segment
	q.Order("seq asc").Find(&segs)
	// skip human-voiced characters and lines locked with audio
	humans := map[string]bool{}
	var hs []model.Character
	a.DB.Where("book_id = ? AND mode = ?", ch.BookID, "human").Find(&hs)
	for _, c := range hs {
		humans[c.ID] = true
	}
	var out []model.Segment
	for _, s := range segs {
		if humans[s.CharacterID] && req.Scope != "segments" {
			continue
		}
		if s.Locked && s.AudioStatus == model.AudioReady && req.Scope != "segments" {
			continue
		}
		if x != nil && !a.CanVoiceCharacter(x, s.CharacterID) {
			continue
		}
		out = append(out, s)
	}
	return out, nil
}

// EstimateGeneration returns segments count, chars and credits for a request.
func (a *App) EstimateGeneration(x *Access, chapterIDs []string, req GenerateRequest) (map[string]any, error) {
	var chs []model.Chapter
	a.DB.Where("book_id = ? AND id IN ?", x.Book.ID, chapterIDs).Find(&chs)
	var segs, chars int
	var credits int64
	providers := map[string]int{}
	cmap := a.charMap(x.Book.ID)
	for i := range chs {
		list, err := a.generationTargets(&chs[i], req, x)
		if err != nil {
			continue
		}
		for _, s := range list {
			n := utf8.RuneCountInString(s.Text)
			segs++
			chars += n
			tier := ""
			if c := cmap[s.CharacterID]; c != nil {
				if vc, err := a.segmentVoice(&s, c, x.StudioID); err == nil {
					tier = vc.Voice.PriceTier
					providers[vc.Voice.Provider] += n
				}
			}
			credits += a.Estimate(FeatTTS, tier, float64(n))
		}
	}
	rate := x.Book.Settings.V.SpeechRate
	if rate <= 0 {
		rate = 220
	}
	b := a.Balance(x.StudioID)
	return map[string]any{"segments": segs, "chars": chars, "credits": credits, "durationMs": int64(chars) * 60000 / int64(rate), "providers": providers, "available": b.Available}, nil
}

func (a *App) charMap(bookID string) map[string]*model.Character {
	var cs []model.Character
	a.DB.Where("book_id = ?", bookID).Find(&cs)
	m := map[string]*model.Character{}
	for i := range cs {
		m[cs[i].ID] = &cs[i]
	}
	return m
}

func (a *App) EnqueueGeneration(x *Access, chapterIDs []string, req GenerateRequest) ([]*model.Job, error) {
	if err := need(x, PermGenerate); err != nil {
		return nil, err
	}
	var chs []model.Chapter
	a.DB.Where("book_id = ? AND id IN ?", x.Book.ID, chapterIDs).Order("\"index\" asc").Find(&chs)
	if len(chs) == 0 {
		return nil, Bad("请选择章节")
	}
	est, _ := a.EstimateGeneration(x, chapterIDs, req)
	if need := est["credits"].(int64); need > 0 {
		if bal := a.Balance(x.StudioID); bal.Available < need {
			return nil, PaymentRequired(fmt.Sprintf("积分不足：本次配音预计需要 %d 积分，当前可用 %d", need, bal.Available), map[string]any{"need": need, "available": bal.Available})
		}
	}
	var out []*model.Job
	for _, ch := range chs {
		targets, err := a.generationTargets(&ch, req, x)
		if err != nil {
			return nil, err
		}
		if len(targets) == 0 {
			continue
		}
		ids := make([]any, len(targets))
		for i, s := range targets {
			ids[i] = s.ID
		}
		a.DB.Model(&model.Segment{}).Where("id IN ?", targetIDs(targets)).Update("audio_status", model.AudioQueued)
		a.emit(x.StudioID, eventSegments(ch.BookID, ch.ID, statusViews(targets, model.AudioQueued)))
		j := &model.Job{Type: "tts_segments", StudioID: x.StudioID, BookID: ch.BookID, ChapterID: ch.ID, RefType: "chapter", RefID: ch.ID,
			Title: fmt.Sprintf("AI 配音 · %s（%d 句）", ch.Title, len(targets)), CreatedBy: x.User.ID, MaxAttempts: 1,
			Payload: model.Map{V: map[string]any{"segmentIds": ids, "regenerate": req.Regenerate, "autoRender": req.AutoRender}}}
		if err := a.Q.Enqueue(j); err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	if len(out) == 0 {
		return nil, Bad("所选范围内没有需要生成的片段")
	}
	return out, nil
}

func targetIDs(segs []model.Segment) []string {
	ids := make([]string, len(segs))
	for i, s := range segs {
		ids[i] = s.ID
	}
	return ids
}

func statusViews(segs []model.Segment, status string) []map[string]any {
	out := make([]map[string]any, len(segs))
	for i, s := range segs {
		out[i] = map[string]any{"id": s.ID, "audioStatus": status}
	}
	return out
}

func (a *App) jobTTSSegments(ctx context.Context, jc *jobs.Context) error {
	return a.ttsSegments(ctx, jc.Job, jc, false)
}

// ttsSegments synthesizes the job's segments; shared by the job and the one-click pipeline.
func (a *App) ttsSegments(ctx context.Context, j *model.Job, jc reporter, skipHold bool) error {
	var b model.Book
	if err := a.DB.First(&b, "id = ?", j.BookID).Error; err != nil {
		return jobs.Permanent(err)
	}
	rawIDs, _ := j.Payload.V["segmentIds"].([]any)
	var ids []string
	for _, v := range rawIDs {
		ids = append(ids, fmt.Sprint(v))
	}
	regenerate, _ := j.Payload.V["regenerate"].(bool)
	var segs []model.Segment
	a.DB.Where("id IN ?", ids).Order("seq asc").Find(&segs)
	if len(segs) == 0 {
		return nil
	}
	chars := a.charMap(b.ID)
	lex := a.lexicon(b.StudioID, b.ID)
	// estimate & hold credits
	var est int64
	for _, s := range segs {
		tier := ""
		if c := chars[s.CharacterID]; c != nil {
			if vc, err := a.segmentVoice(&s, c, b.StudioID); err == nil {
				tier = vc.Voice.PriceTier
			}
		}
		est += a.Estimate(FeatTTS, tier, float64(utf8.RuneCountInString(s.Text)))
	}
	if skipHold {
		est = 0
	}
	if err := a.Hold(j.ID, b.StudioID, est); err != nil {
		a.DB.Model(&model.Segment{}).Where("id IN ?", ids).Updates(map[string]any{"audio_status": model.AudioFailed, "last_error": "积分不足"})
		a.emit(b.StudioID, eventSegments(b.ID, j.ChapterID, statusViews(segs, model.AudioFailed)))
		return jobs.Permanent(err)
	}
	// group by character so each voice renders its lines together, as a cast would record
	sort.SliceStable(segs, func(i, k int) bool { return segs[i].CharacterID < segs[k].CharacterID })
	type result struct {
		seg model.Segment
		err error
		out *synthOutcome
	}
	var (
		mu      sync.Mutex
		done    int
		failed  int
		cached  int
		credits int64
		pending []map[string]any
	)
	flush := func(force bool) {
		mu.Lock()
		defer mu.Unlock()
		if len(pending) == 0 || (!force && len(pending) < 8) {
			return
		}
		a.emit(b.StudioID, eventSegments(b.ID, j.ChapterID, pending))
		pending = nil
	}
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	insufficient := false
	for i := range segs {
		if ctx.Err() != nil {
			break
		}
		s := segs[i]
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			if ctx.Err() != nil || insufficient {
				return
			}
			// per-account concurrency limit
			var lim chan struct{}
			if c := chars[s.CharacterID]; c != nil {
				if vc, err := a.segmentVoice(&s, c, b.StudioID); err == nil {
					var acc model.ProviderAccount
					if a.DB.Select("id", "concurrency").First(&acc, "id = ? OR (provider = ? AND kind = ?)", vc.Voice.ProviderAccountID, vc.Voice.Provider, model.KindTTS).Error == nil {
						lim = a.Q.Limiter("tts:"+acc.ID, acc.Concurrency)
					}
				}
			}
			if lim != nil {
				lim <- struct{}{}
				defer func() { <-lim }()
			}
			a.DB.Model(&model.Segment{}).Where("id = ?", s.ID).Update("audio_status", model.AudioGenerating)
			mu.Lock()
			pending = append(pending, map[string]any{"id": s.ID, "audioStatus": model.AudioGenerating})
			mu.Unlock()
			out, err := a.synthesizeSegment(ctx, &b, &s, chars, lex, j.CreatedBy, j.ID, regenerate)
			if err == nil && b.Settings.V.AutoQC {
				out = a.autoQC(ctx, &b, &s, chars, lex, j.CreatedBy, j.ID, out)
			}
			mu.Lock()
			done++
			if err != nil {
				failed++
				var ce *Error
				if errors.As(err, &ce) && ce.Code == "insufficient_credits" {
					insufficient = true
				}
				a.DB.Model(&model.Segment{}).Where("id = ?", s.ID).Updates(map[string]any{"audio_status": model.AudioFailed, "last_error": truncateStr(err.Error(), 480)})
				pending = append(pending, map[string]any{"id": s.ID, "audioStatus": model.AudioFailed, "lastError": truncateStr(err.Error(), 200)})
			} else {
				if out.Cached {
					cached++
				}
				credits += out.Credits
				var fresh model.Segment
				a.DB.First(&fresh, "id = ?", s.ID)
				pending = append(pending, a.SegmentViews([]model.Segment{fresh})[0])
			}
			n := done
			mu.Unlock()
			jc.Progress(float64(n)/float64(len(segs)), fmt.Sprintf("已完成 %d/%d 句", n, len(segs)))
			flush(false)
		}()
	}
	wg.Wait()
	flush(true)
	if ctx.Err() != nil {
		a.DB.Model(&model.Segment{}).Where("id IN ? AND audio_status IN ?", ids, []string{model.AudioQueued, model.AudioGenerating}).Update("audio_status", model.AudioNone)
		a.emit(b.StudioID, eventChapter(b.ID, j.ChapterID, "script"))
		return ctx.Err()
	}
	jc.SetResult(map[string]any{"done": done - failed, "failed": failed, "cached": cached, "credits": credits})
	msg := fmt.Sprintf("生成完成：成功 %d 句", done-failed)
	if cached > 0 {
		msg += fmt.Sprintf("（其中 %d 句复用缓存，免费）", cached)
	}
	if failed > 0 {
		msg += fmt.Sprintf("，失败 %d 句", failed)
	}
	jc.Progress(1, msg)
	a.emit(b.StudioID, eventChapter(b.ID, j.ChapterID, "audio"))
	if auto, _ := j.Payload.V["autoRender"].(bool); auto && failed == 0 {
		a.enqueueRender(b.StudioID, b.ID, j.ChapterID, j.CreatedBy, "")
	}
	if failed > 0 && failed == done {
		return jobs.Permanent(fmt.Errorf("全部 %d 句生成失败，请查看片段上的错误信息", failed))
	}
	return nil
}

// autoQC transcribes a generated take and retries (free) when the text clearly mismatches.
func (a *App) autoQC(ctx context.Context, b *model.Book, s *model.Segment, chars map[string]*model.Character, lex []model.LexiconEntry, userID, jobID string, out *synthOutcome) *synthOutcome {
	prov, _, err := a.ASRFor(b.StudioID)
	if err != nil || out == nil || out.Cached {
		return out
	}
	for attempt := 0; attempt < 3; attempt++ {
		path, _, err := a.AssetPath(out.Take.AssetID)
		if err != nil {
			return out
		}
		res, err := prov.Transcribe(ctx, path, b.Language)
		if err != nil {
			return out
		}
		findings, score := align.Compare(s.Text, res.Text)
		if score >= 0.85 || attempt == 2 {
			qc, note := "pass", ""
			if score < 0.85 {
				qc = "suspect"
				var parts []string
				for _, f := range findings {
					switch f.Type {
					case "missing":
						parts = append(parts, "漏读「"+f.Expected+"」")
					case "extra":
						parts = append(parts, "多读「"+f.Actual+"」")
					case "wrong":
						parts = append(parts, "「"+f.Expected+"」读成「"+f.Actual+"」")
					}
				}
				note = truncateStr(strings.Join(parts, "；"), 480)
			}
			a.DB.Model(out.Take).Updates(map[string]any{"qc": qc, "qc_detail": model.Map{V: map[string]any{"score": score, "heard": res.Text}}})
			a.DB.Model(&model.Segment{}).Where("id = ?", s.ID).Updates(map[string]any{"qc": qc, "qc_note": note})
			return out
		}
		// free automatic retry
		retry, err := a.synthesizeSegment(ctx, b, s, chars, lex, userID, jobID, true)
		if err != nil {
			return out
		}
		out = retry
	}
	return out
}

// regenSeed gives explicit re-generations a fresh random seed so deterministic providers produce a new rendition.
func regenSeed(regenerate bool) int {
	if !regenerate {
		return 0
	}
	return 1 + rand.IntN(65535)
}

// ---------- previews ----------

// Preview synthesizes a short audition clip for a voice (cached by content).
func (a *App) Preview(ctx context.Context, x *Access, v *model.Voice, txt, emotion string, speed, pitch float64) (string, error) {
	txt = strings.TrimSpace(txt)
	// The default self-introduction of a platform voice without an official preview becomes that voice's
	// preview for everyone: generated once, free of charge.
	firstSample := txt == "" && (emotion == "" || emotion == "neutral") && speed == 0 && pitch == 0 &&
		v.Scope == model.ScopeSystem && v.SampleAssetID == "" && v.SampleURL == ""
	if txt == "" {
		txt = firstNonEmpty(v.SampleText, "你好，很高兴认识你。这是我的声音，希望你会喜欢。")
	}
	if utf8.RuneCountInString(txt) > 300 {
		return "", Bad("试听文本最多 300 字")
	}
	if speed == 0 {
		speed = firstNonZero(v.Defaults.V.Speed, 1)
	}
	provider, acc, err := a.TTSFor(v)
	if err != nil {
		return "", err
	}
	// cached per account (like segment takes): audio from one account is never served for another
	key := hashParts("preview", acc.ID, v.ID, txt, emotion, fmt.Sprintf("%.2f|%.2f", speed, pitch))
	var cached model.Asset
	if a.DB.Where("kind = ? AND checksum = ?", model.AssetPreview, key).First(&cached).Error == nil && a.Store.Exists(cached.StorageKey) {
		if firstSample {
			a.adoptVoiceSample(v, &cached)
		}
		return a.AssetURL(&cached), nil
	}
	if !a.Allow("preview:"+x.User.ID, 30) {
		return "", TooMany("试听过于频繁，请稍后再试")
	}
	if !firstSample {
		if _, err := a.ChargeCredits(Charge{StudioID: x.StudioID, UserID: x.User.ID, Feature: FeatPreview, Quantity: 1, Unit: "calls", Provider: acc.Provider, AccountID: acc.ID}); err != nil {
			return "", err
		}
	}
	ttsText, prons := BuildTTSInput(txt, nil, a.lexicon(x.StudioID, ""), tts.CapsFor(provider, v.Model, v.ProviderVoiceID).Phoneme)
	out, err := provider.Synthesize(ctx, tts.SynthesizeRequest{Text: ttsText, ProviderVoiceID: v.ProviderVoiceID, Model: v.Model, Emotion: emotion, Speed: speed, Pitch: pitch, Prons: prons,
		VoiceGender: v.Gender, VoiceAge: v.AgeGroup, VoiceSeed: v.ID, Intensity: 2})
	if err != nil {
		return "", withDetail("试听生成失败", err)
	}
	a.voiceUsed(v, provider)
	asset, _, err := a.storeSpeech(ctx, x.StudioID, x.User.ID, out, model.AssetPreview, map[string]any{"voiceId": v.ID})
	if err != nil {
		return "", err
	}
	a.DB.Model(asset).Update("checksum", key)
	if firstSample {
		a.adoptVoiceSample(v, asset)
	}
	return a.AssetURL(asset), nil
}

// adoptVoiceSample makes a generated preview the platform voice's shared sample.
func (a *App) adoptVoiceSample(v *model.Voice, asset *model.Asset) {
	a.DB.Model(asset).Updates(map[string]any{"scope": model.ScopeSystem, "studio_id": "", "name": v.Name + " 试听"})
	a.DB.Model(&model.Voice{}).Where("id = ? AND sample_asset_id = ''", v.ID).Update("sample_asset_id", asset.ID)
}

func firstNonZero(vals ...float64) float64 {
	for _, v := range vals {
		if v != 0 {
			return v
		}
	}
	return 0
}
