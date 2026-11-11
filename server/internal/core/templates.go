package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"voicebook/internal/ai/tts"
	"voicebook/internal/audio"
	dbx "voicebook/internal/db"
	"voicebook/internal/model"
)

// Intro/outro templates come in two modes:
//   - compose: a music bed plus an announced text that may name the book and chapter;
//   - audio:   one fixed audio file (uploaded, picked from the library, or generated once from a
//     compose recipe) that plays identically at the start or end of every chapter.
//
// Announcements are cached by voice + text, so a chapter re-render or a text shared by every
// chapter is synthesised (and charged) once, and every episode gets the very same take.

const (
	maxJingleMs   = 3 * 60 * 1000 // longest fixed intro/outro
	maxTplTextLen = 300
	jingleRMSDB   = -19 // fixed audio is levelled close to the dialogue (-20 dBFS RMS)
)

var tplPlaceholder = regexp.MustCompile(`\{([^{}\s]{1,8})\}`)

// errTemplateGone marks a book pointing at a template that was deleted; rendering just skips it.
var errTemplateGone = errors.New("template gone")

// templateVars are the placeholders a compose template may use.
func (a *App) templateVars(b *model.Book, ch *model.Chapter) map[string]string {
	return map[string]string{"书名": b.Title, "作者": b.Author, "章节号": fmt.Sprint(ch.Index), "章节名": ch.Title, "主播": b.Narrator, "出品方": b.Publisher}
}

func fillTemplate(text string, vars map[string]string) string {
	for k, v := range vars {
		text = strings.ReplaceAll(text, "{"+k+"}", v)
	}
	return text
}

// TemplatePlaceholders lists the placeholders used in a text, e.g. ["书名", "章节名"].
func TemplatePlaceholders(text string) []string {
	var out []string
	for _, m := range tplPlaceholder.FindAllStringSubmatch(text, -1) {
		out = append(out, m[1])
	}
	return out
}

func clampF(v, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, v)) }

func clampI(v, lo, hi int64) int64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// NormalizeTemplate validates a studio template before saving it.
func (a *App) NormalizeTemplate(studioID string, t *model.AudioTemplate) error {
	if t.Kind != "outro" {
		t.Kind = "intro"
	}
	if t.Mode != model.TplAudio {
		t.Mode = model.TplCompose
	}
	t.Name = strings.TrimSpace(t.Name)
	if t.Name == "" {
		t.Name = map[string]string{"intro": "我的片头", "outro": "我的片尾"}[t.Kind]
	}
	if utf8.RuneCountInString(t.Name) > 30 {
		return Bad("模板名称最多 30 个字")
	}
	t.Text = strings.TrimSpace(t.Text)
	if utf8.RuneCountInString(t.Text) > maxTplTextLen {
		return Bad("播报文字最多 %d 字", maxTplTextLen)
	}
	t.MusicVolume = clampF(t.MusicVolume, -30, 0)
	t.LeadMs, t.TailMs = clampI(t.LeadMs, 0, 15000), clampI(t.TailMs, 0, 15000)
	t.AudioGain = clampF(t.AudioGain, -12, 6)
	usable := func(id, what string, audioOnly bool) error {
		if id == "" {
			return nil
		}
		var as model.Asset
		if a.DB.First(&as, "id = ? AND (scope = ? OR studio_id = ?)", id, model.ScopeSystem, studioID).Error != nil {
			return Bad("%s不存在，或不属于这个工作室", what)
		}
		if audioOnly && (as.Kind == model.AssetImage || as.Kind == model.AssetDocument || as.DurationMs <= 0) {
			return Bad("%s不是可播放的音频", what)
		}
		if what == "固定音频" && as.DurationMs > maxJingleMs {
			return Bad("固定音频最长 %d 分钟，这段有 %s", maxJingleMs/60000, fmtMs(as.DurationMs))
		}
		return nil
	}
	if err := usable(t.MusicAssetID, "背景音乐", true); err != nil {
		return err
	}
	if t.Mode == model.TplAudio {
		if t.AudioAssetID == "" {
			return Bad("请上传、选择或生成一段固定音频")
		}
		return usable(t.AudioAssetID, "固定音频", true)
	}
	if t.Text == "" && t.MusicAssetID == "" {
		return Bad("请填写播报文字，或者选择一段背景音乐")
	}
	return nil
}

func fmtMs(ms int64) string {
	s := ms / 1000
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

// TemplateUsage counts the books (and whether the studio default) using a template.
func (a *App) TemplateUsage(studioID, id string) (books int64, studioDefault bool) {
	a.DB.Model(&model.Book{}).Where("studio_id = ? AND deleted_at IS NULL AND ("+a.tplUses()+")", studioID, id, id).Count(&books)
	var st model.Studio
	if a.DB.Select("settings").First(&st, "id = ?", studioID).Error == nil {
		studioDefault = st.Settings.V.IntroTemplateID == id || st.Settings.V.OutroTemplateID == id
	}
	return
}

// renderTemplate builds the intro/outro of one chapter from a stored template.
func (a *App) renderTemplate(ctx context.Context, b *model.Book, templateID string, vars map[string]string, extra, out string) (string, int64, error) {
	var t model.AudioTemplate
	if a.DB.First(&t, "id = ?", templateID).Error != nil {
		return "", 0, errTemplateGone
	}
	return a.renderTemplateWith(ctx, b, &t, vars, extra, out)
}

// renderTemplateWith renders a (possibly unsaved) template. extra is a statement that must be
// announced as well (the platform's AI disclosure).
func (a *App) renderTemplateWith(ctx context.Context, b *model.Book, t *model.AudioTemplate, vars map[string]string, extra, out string) (string, int64, error) {
	if t.Mode == model.TplAudio {
		return a.renderFixedTemplate(ctx, b, t, extra, out)
	}
	txt := fillTemplate(t.Text, vars)
	if extra != "" || t.Disclaimer {
		if extra == "" {
			var label AILabelSettings
			a.Setting("ai_label", &label)
			extra = label.Text
		}
		txt = strings.TrimSpace(txt + " " + extra)
	}
	dir := filepath.Dir(out)
	speech := ""
	var speechMs int64
	if strings.TrimSpace(txt) != "" {
		var err error
		speech, speechMs, err = a.speak(ctx, b, t.VoiceID, txt, filepath.Join(dir, "tpl-"+t.Kind+"-speech.wav"))
		if err != nil {
			return "", 0, err
		}
	}
	lead, tail := t.LeadMs, t.TailMs
	if t.MusicAssetID == "" {
		lead, tail = 300, 500
	}
	total := lead + speechMs + tail
	var clips []audio.MixClip
	if t.MusicAssetID != "" {
		if p, _, err := a.AssetPath(t.MusicAssetID); err == nil {
			clips = append(clips, audio.MixClip{Source: p, StartMs: 0, DurationMs: total, Loop: true, GainDB: t.MusicVolume, FadeInMs: 400, FadeOutMs: 900, Duck: true})
		}
	}
	if speech != "" {
		clips = append(clips, audio.MixClip{Source: speech, StartMs: lead, Voice: true})
	}
	if len(clips) == 0 {
		return "", 0, nil
	}
	ms, err := a.FF.Mix(ctx, clips, out, audio.MixOptions{DuckDB: -10, TotalMs: total})
	return out, ms, err
}

// renderFixedTemplate plays the template's audio as-is (levelled to the dialogue), followed by
// the AI disclosure when the platform requires it to be spoken.
func (a *App) renderFixedTemplate(ctx context.Context, b *model.Book, t *model.AudioTemplate, extra, out string) (string, int64, error) {
	p, as, err := a.AssetPath(t.AudioAssetID)
	if err != nil || !a.Store.Exists(as.StorageKey) {
		return "", 0, Bad("「%s」的固定音频文件不见了，请在资产库 → 片头片尾模板里重新上传或生成", t.Name)
	}
	pcm, err := a.FF.DecodePCM(ctx, p, audio.SampleRate, 2, "")
	if err != nil {
		return "", 0, fmt.Errorf("读取「%s」的音频失败：%w", t.Name, err)
	}
	if pcm.Frames() == 0 {
		return "", 0, Bad("「%s」的固定音频是空的", t.Name)
	}
	audio.NormalizeRMS(pcm, jingleRMSDB+clampF(t.AudioGain, -12, 6), -1)
	extra = strings.TrimSpace(extra)
	if extra == "" {
		return out, pcm.DurationMs(), audio.WriteWAVFile(out, pcm)
	}
	dir := filepath.Dir(out)
	jingle := filepath.Join(dir, "tpl-"+t.Kind+"-fixed.wav")
	if err := audio.WriteWAVFile(jingle, pcm); err != nil {
		return "", 0, err
	}
	speech, speechMs, err := a.speak(ctx, b, "", extra, filepath.Join(dir, "tpl-"+t.Kind+"-label.wav"))
	if err != nil {
		return "", 0, err
	}
	jm := pcm.DurationMs()
	total := jm + 300 + speechMs + 400
	ms, err := a.FF.Mix(ctx, []audio.MixClip{{Source: jingle, StartMs: 0}, {Source: speech, StartMs: jm + 300, Voice: true}}, out, audio.MixOptions{TotalMs: total})
	return out, ms, err
}

func (a *App) renderAnnouncement(ctx context.Context, b *model.Book, txt, out string) (string, int64, error) {
	p, ms, err := a.speak(ctx, b, "", txt, out)
	if err != nil {
		return "", 0, err
	}
	return p, ms + 400, nil
}

// announcer picks the voice of an announcement: the template's voice, else the book narrator,
// else the platform's recommended official voice.
func (a *App) announcer(b *model.Book, voiceID string) (model.Voice, error) {
	var v model.Voice
	if voiceID != "" && a.DB.First(&v, "id = ? AND status = ?", voiceID, model.VoiceReady).Error == nil {
		return v, nil
	}
	var narr model.Character
	if b.ID != "" && a.DB.First(&narr, "book_id = ? AND kind = ?", b.ID, model.CharNarrator).Error == nil && narr.VoiceID != "" {
		if a.DB.First(&v, "id = ? AND status = ?", narr.VoiceID, model.VoiceReady).Error == nil {
			return v, nil
		}
	}
	if a.DB.Where("scope = ? AND status = ? AND listed = ? AND provider IN ?", model.ScopeSystem, model.VoiceReady, true, a.ActiveTTSProviders()).Order("CASE WHEN provider = 'placeholder' THEN 1 ELSE 0 END, recommended desc, sort_order asc").First(&v).Error != nil {
		return v, Bad("没有可用于播报的音色")
	}
	return v, nil
}

// speak synthesises an announcement. Takes are cached per studio by voice and final TTS input,
// so the same text is only synthesised and charged once.
func (a *App) speak(ctx context.Context, b *model.Book, voiceID, txt, out string) (string, int64, error) {
	v, err := a.announcer(b, voiceID)
	if err != nil {
		return "", 0, err
	}
	p, acc, err := a.TTSFor(&v)
	if err != nil {
		return "", 0, err
	}
	ttsText, prons := BuildTTSInput(txt, nil, a.lexicon(b.StudioID, b.ID), tts.CapsFor(p, v.Model, v.ProviderVoiceID).Phoneme)
	speed := firstNonZero(v.Defaults.V.Speed, 1)
	key := hashParts("announce", acc.ID, v.ID, v.ProviderVoiceID, v.Model, ttsText, fmt.Sprint(prons), fmt.Sprint(speed)) // per account, like segment takes
	var cached model.Asset
	if a.DB.Where("kind = ? AND studio_id = ? AND checksum = ?", model.AssetPreview, b.StudioID, key).First(&cached).Error == nil && a.Store.Exists(cached.StorageKey) {
		if ms, ok := cached.Meta.V["durationMs"].(float64); ok && ms > 0 {
			return a.Store.Path(cached.StorageKey), int64(ms), nil
		}
	}
	res, err := p.Synthesize(ctx, tts.SynthesizeRequest{Text: ttsText, ProviderVoiceID: v.ProviderVoiceID, Model: v.Model, Speed: speed, Prons: prons, VoiceGender: v.Gender, VoiceAge: v.AgeGroup, VoiceSeed: v.ID})
	if err != nil {
		return "", 0, err
	}
	a.voiceUsed(&v, p)
	a.ChargeCredits(Charge{StudioID: b.StudioID, BookID: b.ID, Feature: FeatTTS, Tier: v.PriceTier, Quantity: float64(utf8.RuneCountInString(ttsText)), Unit: "chars", Provider: acc.Provider, AccountID: acc.ID, Note: "片头片尾播报"})
	var pcm *audio.PCM
	if res.Format == "wav" {
		pcm, err = audio.DecodeWAV(bytes.NewReader(res.Data))
	} else {
		tmp := out + "." + res.Format
		os.WriteFile(tmp, res.Data, 0o644)
		pcm, err = a.FF.DecodePCM(ctx, tmp, audio.SampleRate, 1, "")
	}
	if err != nil {
		return "", 0, err
	}
	pcm = audio.TrimSilence(audio.Resample(audio.ToMono(pcm), audio.SampleRate), -48, 40)
	audio.NormalizeRMS(pcm, -20, -2)
	if err := audio.WriteWAVFile(out, pcm); err != nil {
		return "", 0, err
	}
	ms := pcm.DurationMs()
	if b.StudioID != "" {
		if as, err := a.SaveAssetFile(ctx, AssetInput{StudioID: b.StudioID, Kind: model.AssetPreview, Name: "片头片尾播报", Ext: "wav", Origin: "generated", NoPeaks: true, NoProbe: true,
			Meta: map[string]any{"durationMs": ms, "voiceId": v.ID, "text": truncateStr(txt, 200)}}, out); err == nil {
			a.DB.Model(as).Updates(map[string]any{"checksum": key, "duration_ms": ms})
		}
	}
	return out, ms, nil
}

// TemplatePreview renders a template (saved or still being edited) with sample variables.
func (a *App) TemplatePreview(ctx context.Context, x *Access, t *model.AudioTemplate, b *model.Book) (string, error) {
	if t.Mode == model.TplAudio {
		if t.AudioAssetID == "" {
			return "", Bad("还没有设置固定音频")
		}
		var as model.Asset
		if a.DB.First(&as, "id = ?", t.AudioAssetID).Error != nil {
			return "", Bad("固定音频文件不见了，请重新上传或生成")
		}
		return a.AssetURL(&as), nil
	}
	if !a.Allow("tplprev:"+x.User.ID, 30) {
		return "", TooMany("试听过于频繁，请稍后再试")
	}
	dir, err := a.Store.TempDir("tplprev")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	if b == nil {
		b = &model.Book{StudioID: x.StudioID, Title: "示例作品", Author: "示例作者"}
	}
	vars := map[string]string{"书名": b.Title, "作者": firstNonEmpty(b.Author, "佚名"), "章节号": "1", "章节名": "第一章", "主播": firstNonEmpty(b.Narrator, "主播"), "出品方": firstNonEmpty(b.Publisher, a.Site().Name)}
	out := filepath.Join(dir, "preview.wav")
	p, _, err := a.renderTemplateWith(ctx, b, t, vars, "", out)
	if err != nil || p == "" {
		return "", firstErr(err, Bad("模板内容为空"))
	}
	asset, err := a.SaveAssetFile(ctx, AssetInput{StudioID: x.StudioID, Kind: model.AssetPreview, Name: "模板试听", Ext: "wav", Origin: "generated", CreatedBy: x.User.ID, NoPeaks: true}, p)
	if err != nil {
		return "", err
	}
	return a.AssetURL(asset), nil
}

// GenerateTemplateAudio renders a compose recipe once and stores it as a fixed intro/outro audio.
// The result is identical in every chapter and book, so placeholders are not allowed.
func (a *App) GenerateTemplateAudio(ctx context.Context, x *Access, t model.AudioTemplate) (*model.Asset, error) {
	if ph := TemplatePlaceholders(t.Text); len(ph) > 0 {
		return nil, Bad("固定音频每一集都完全一样，不能用 {%s} 这类占位符，请直接写成文字", ph[0])
	}
	t.Mode, t.ID = model.TplCompose, ""
	if err := a.NormalizeTemplate(x.StudioID, &t); err != nil {
		return nil, err
	}
	if !a.Allow("tplgen:"+x.User.ID, 20) {
		return nil, TooMany("生成过于频繁，请稍后再试")
	}
	dir, err := a.Store.TempDir("tplgen")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	b := &model.Book{StudioID: x.StudioID}
	p, ms, err := a.renderTemplateWith(ctx, b, &t, nil, "", filepath.Join(dir, "jingle.wav"))
	if err != nil {
		return nil, err
	}
	if p == "" || ms <= 0 {
		return nil, Bad("模板内容为空")
	}
	kind := map[string]string{"intro": "片头", "outro": "片尾"}[t.Kind]
	return a.SaveAssetFile(ctx, AssetInput{StudioID: x.StudioID, OwnerUserID: x.User.ID, Kind: model.AssetJingle, Name: firstNonEmpty(t.Name, "我的"+kind) + " · AI 生成", Ext: "wav", Origin: "generated", License: "commercial", CreatedBy: x.User.ID,
		Meta: map[string]any{"text": t.Text, "voiceId": t.VoiceID, "musicAssetId": t.MusicAssetID}}, p)
}

// tplUses matches books whose settings pick a template (two placeholders: the id twice).
func (a *App) tplUses() string {
	return dbx.JSONText(a.DB, "settings", "introTemplateId") + " = ? OR " + dbx.JSONText(a.DB, "settings", "outroTemplateId") + " = ?"
}

// MarkTemplateStale flags rendered chapters of the books using an edited template for re-render.
func (a *App) MarkTemplateStale(studioID, id string) {
	books := a.DB.Model(&model.Book{}).Select("id").Where("studio_id = ? AND ("+a.tplUses()+")", studioID, id, id)
	a.DB.Model(&model.Chapter{}).Where("book_id IN (?) AND render_id <> ''", books).Update("render_stale", true)
}

// ApplyBookTemplates switches the intro/outro on for every chapter when a book starts using a
// template, so "every episode" includes chapters mixed before the template was chosen.
// Switching between templates keeps chapters the user turned off.
func (a *App) ApplyBookTemplates(bookID string, old, cur model.BookSettings) {
	chapters := a.DB.Model(&model.Chapter{}).Select("id").Where("book_id = ?", bookID)
	if old.IntroTemplateID == "" && cur.IntroTemplateID != "" {
		a.DB.Model(&model.Mix{}).Where("chapter_id IN (?)", chapters).Update("intro", true)
	}
	if old.OutroTemplateID == "" && cur.OutroTemplateID != "" {
		a.DB.Model(&model.Mix{}).Where("chapter_id IN (?)", chapters).Update("outro", true)
	}
}
