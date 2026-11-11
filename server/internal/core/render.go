package core

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"time"
	"unicode/utf8"

	"voicebook/internal/audio"
	"voicebook/internal/jobs"
	"voicebook/internal/model"
)

type TLClip struct {
	SegmentID   string   `json:"segmentId"`
	CharacterID string   `json:"characterId"`
	Type        string   `json:"type"`
	Text        string   `json:"text"`
	StartMs     int64    `json:"startMs"`
	DurationMs  int64    `json:"durationMs"`
	GapMs       int64    `json:"gapMs"`
	AssetID     string   `json:"assetId"`
	URL         string   `json:"url,omitempty"`
	Missing     bool     `json:"missing"`
	Effects     []string `json:"effects"`
	VolumeDB    float64  `json:"volumeDb"`
}

type TLTrackClip struct {
	model.Clip
	AbsStartMs int64  `json:"absStartMs"`
	AbsDurMs   int64  `json:"absDurationMs"`
	URL        string `json:"url"`
	AssetDurMs int64  `json:"assetDurationMs"`
}

type TLTrack struct {
	model.Track
	Resolved []TLTrackClip `json:"resolved"`
}

type Timeline struct {
	ChapterID  string    `json:"chapterId"`
	Dialogue   []TLClip  `json:"dialogue"`
	Sfx        []TLClip  `json:"sfx"`
	Tracks     []TLTrack `json:"tracks"`
	DurationMs int64     `json:"durationMs"`
	HeadMs     int64     `json:"headMs"`
	TailMs     int64     `json:"tailMs"`
	Missing    int       `json:"missing"`
	DuckingDb  float64   `json:"duckingDb"`
	Intro      bool      `json:"intro"`
	Outro      bool      `json:"outro"`
	MixVersion int       `json:"mixVersion"`
}

func (a *App) mixFor(ch *model.Chapter) *model.Mix {
	var m model.Mix
	if a.DB.First(&m, "chapter_id = ?", ch.ID).Error != nil {
		var b model.Book
		a.DB.First(&b, "id = ?", ch.BookID)
		m = model.Mix{ChapterID: ch.ID, DuckingDb: firstNonZero(b.Settings.V.DuckingDb, -12), Intro: b.Settings.V.IntroTemplateID != "", Outro: b.Settings.V.OutroTemplateID != ""}
		m.Tracks.V = []model.Track{
			{ID: "bgm", Kind: "bgm", Name: "背景音乐", Volume: -6, Duck: true, Clips: []model.Clip{}},
			{ID: "sfx", Kind: "sfx", Name: "音效", Volume: 0, Clips: []model.Clip{}},
			{ID: "ambience", Kind: "ambience", Name: "环境音", Volume: -8, Duck: true, Clips: []model.Clip{}},
		}
		a.DB.Create(&m)
	}
	return &m
}

// BuildTimeline lays out dialogue from the script (pause rules + per-line overrides) and resolves mix tracks.
func (a *App) BuildTimeline(ch *model.Chapter, withURLs bool) (*Timeline, error) {
	if ch.CurrentScriptID == "" {
		return nil, Bad("该章节还没有画本")
	}
	var b model.Book
	a.DB.First(&b, "id = ?", ch.BookID)
	rules := b.Settings.V.Pause
	if rules.SameSpeakerMs == 0 {
		rules = model.DefaultPauseRules()
	}
	rate := b.Settings.V.SpeechRate
	if rate <= 0 {
		rate = 220
	}
	segs := a.scriptSegments(ch.CurrentScriptID)
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
	assetCache := map[string]*model.Asset{}
	getAsset := func(id string) *model.Asset {
		if id == "" {
			return nil
		}
		if as, ok := assetCache[id]; ok {
			return as
		}
		var as model.Asset
		if a.DB.First(&as, "id = ?", id).Error != nil {
			assetCache[id] = nil
			return nil
		}
		assetCache[id] = &as
		return &as
	}
	tl := &Timeline{ChapterID: ch.ID, HeadMs: int64(rules.ChapterHeadMs), TailMs: int64(rules.ChapterTailMs)}
	cursor := tl.HeadMs
	var prev *model.Segment
	for i := range segs {
		s := &segs[i]
		if s.Removed {
			continue
		}
		switch s.Type {
		case model.SegPause:
			cursor += int64(intOr(s.PauseAfterMs, 1000))
			continue
		case model.SegSfx:
			as := getAsset(s.SfxAssetID)
			if as == nil {
				continue
			}
			c := TLClip{SegmentID: s.ID, Type: s.Type, Text: s.Text, StartMs: cursor, DurationMs: as.DurationMs, AssetID: as.ID, Effects: s.Effects.V, VolumeDB: f64(s.Volume, 0)}
			if withURLs {
				c.URL = a.AssetURL(as)
			}
			tl.Sfx = append(tl.Sfx, c)
			adv := as.DurationMs * 6 / 10
			if adv > 2000 {
				adv = 2000
			}
			cursor += adv
			continue
		}
		gap := int64(0)
		if prev != nil {
			switch {
			case prev.PauseAfterMs != nil:
				gap = int64(*prev.PauseAfterMs)
			case s.PauseBeforeMs != nil:
				gap = int64(*s.PauseBeforeMs)
			case s.SceneBreak:
				gap = int64(rules.SceneMs)
			case s.Para != prev.Para:
				gap = int64(rules.ParagraphMs)
			case s.CharacterID != prev.CharacterID:
				gap = int64(rules.SpeakerChangeMs)
			default:
				gap = int64(rules.SameSpeakerMs)
			}
		} else if s.PauseBeforeMs != nil {
			gap = int64(*s.PauseBeforeMs)
		}
		cursor += gap
		c := TLClip{SegmentID: s.ID, CharacterID: s.CharacterID, Type: s.Type, Text: s.Text, StartMs: cursor, GapMs: gap, Effects: s.Effects.V, VolumeDB: f64(s.Volume, 0)}
		if len(c.Effects) == 0 && s.Type == model.SegMonologue && b.Settings.V.MonologueReverb {
			c.Effects = []string{"monologue"}
		}
		t, ok := takes[s.SelectedTakeID]
		as := (*model.Asset)(nil)
		if ok {
			as = getAsset(t.AssetID)
		}
		if ok && as != nil && s.AudioStatus != model.AudioFailed {
			c.AssetID = as.ID
			c.DurationMs = int64(t.DurationMs)
			if c.DurationMs == 0 {
				c.DurationMs = as.DurationMs
			}
			if withURLs {
				c.URL = a.AssetURL(as)
			}
		} else {
			c.Missing = true
			c.DurationMs = int64(utf8.RuneCountInString(s.Text)) * 60000 / int64(rate)
			tl.Missing++
		}
		tl.Dialogue = append(tl.Dialogue, c)
		cursor += c.DurationMs
		prev = s
	}
	if prev != nil && prev.PauseAfterMs != nil {
		cursor += int64(*prev.PauseAfterMs)
	}
	speechEnd := cursor
	tl.DurationMs = speechEnd + tl.TailMs
	// mix tracks
	mix := a.mixFor(ch)
	tl.DuckingDb, tl.Intro, tl.Outro, tl.MixVersion = mix.DuckingDb, mix.Intro, mix.Outro, mix.Version
	starts := map[string]int64{}
	for _, d := range tl.Dialogue {
		starts[d.SegmentID] = d.StartMs
	}
	for _, d := range tl.Sfx {
		starts[d.SegmentID] = d.StartMs
	}
	for _, tr := range mix.Tracks.V {
		t := TLTrack{Track: tr}
		for _, c := range tr.Clips {
			as := getAsset(c.AssetID)
			if as == nil {
				continue
			}
			abs := c.StartMs
			if c.AnchorSegment != "" {
				if st, ok := starts[c.AnchorSegment]; ok {
					abs = st + c.StartMs
				}
			}
			dur := c.DurationMs
			if c.Fill {
				dur = tl.DurationMs - abs
			} else if dur <= 0 {
				dur = as.DurationMs - c.OffsetMs
			}
			if dur <= 0 {
				continue
			}
			rc := TLTrackClip{Clip: c, AbsStartMs: abs, AbsDurMs: dur, AssetDurMs: as.DurationMs}
			if withURLs {
				rc.URL = a.AssetURL(as)
			}
			t.Resolved = append(t.Resolved, rc)
			if abs+dur > tl.DurationMs && !c.Fill {
				tl.DurationMs = abs + dur
			}
		}
		if t.Resolved == nil {
			t.Resolved = []TLTrackClip{}
		}
		tl.Tracks = append(tl.Tracks, t)
	}
	return tl, nil
}

func intOr(p *int, def int) int {
	if p == nil {
		return def
	}
	return *p
}

// SaveMix stores user-arranged tracks (bgm/sfx/ambience) for a chapter.
func (a *App) SaveMix(x *Access, ch *model.Chapter, tracks []model.Track, duck float64, intro, outro bool, version int) (*model.Mix, error) {
	if err := need(x, PermPost); err != nil {
		return nil, err
	}
	m := a.mixFor(ch)
	if version != 0 && version != m.Version {
		return nil, Conflict("时间轴已被其他成员修改，请刷新后再保存")
	}
	for i := range tracks {
		if tracks[i].ID == "" {
			tracks[i].ID = model.NewID()
		}
		if tracks[i].Clips == nil {
			tracks[i].Clips = []model.Clip{}
		}
		for k := range tracks[i].Clips {
			if tracks[i].Clips[k].ID == "" {
				tracks[i].Clips[k].ID = model.NewID()
			}
		}
	}
	m.Tracks.V = tracks
	m.DuckingDb, m.Intro, m.Outro = duck, intro, outro
	m.Version++
	m.UpdatedBy = x.User.ID
	if err := a.DB.Save(m).Error; err != nil {
		return nil, err
	}
	a.markRenderStale(ch.ID)
	a.emit(x.StudioID, eventChapter(ch.BookID, ch.ID, "mix"))
	return m, nil
}

// ---------- render job ----------

func (a *App) EnqueueRender(x *Access, ch *model.Chapter, preset string, allowMissing bool) (*model.Job, error) {
	if err := need(x, PermPost); err != nil {
		if err2 := need(x, PermGenerate); err2 != nil {
			return nil, err
		}
	}
	tl, err := a.BuildTimeline(ch, false)
	if err != nil {
		return nil, err
	}
	if tl.Missing > 0 && !allowMissing {
		return nil, &Error{Status: 409, Code: "missing_audio", Msg: fmt.Sprintf("还有 %d 句没有音频，请先完成配音（或选择跳过缺失片段合成试听版）", tl.Missing), Data: map[string]any{"missing": tl.Missing}}
	}
	return a.enqueueRenderOpts(x.StudioID, ch.BookID, ch.ID, x.User.ID, preset, allowMissing)
}

func (a *App) enqueueRender(studioID, bookID, chapterID, userID, preset string) (*model.Job, error) {
	return a.enqueueRenderOpts(studioID, bookID, chapterID, userID, preset, false)
}

func (a *App) enqueueRenderOpts(studioID, bookID, chapterID, userID, preset string, allowMissing bool) (*model.Job, error) {
	var ch model.Chapter
	a.DB.Select("title").First(&ch, "id = ?", chapterID)
	j := &model.Job{Type: "render", StudioID: studioID, BookID: bookID, ChapterID: chapterID, RefType: "chapter", RefID: chapterID, Title: "合成成品 · " + ch.Title, CreatedBy: userID, MaxAttempts: 2,
		Payload: model.Map{V: map[string]any{"preset": preset, "allowMissing": allowMissing}}}
	return j, a.Q.Enqueue(j)
}

func (a *App) jobRender(ctx context.Context, jc *jobs.Context) error {
	return a.renderChapter(ctx, jc.Job, jc)
}

// renderChapter mixes and encodes a chapter; shared by the job, exports and the pipeline.
func (a *App) renderChapter(ctx context.Context, j *model.Job, jc reporter) error {
	var ch model.Chapter
	if err := a.DB.First(&ch, "id = ?", j.ChapterID).Error; err != nil {
		return jobs.Permanent(err)
	}
	var b model.Book
	a.DB.First(&b, "id = ?", ch.BookID)
	presetKey, _ := j.Payload.V["preset"].(string)
	if presetKey == "" {
		presetKey = b.Settings.V.LoudnessPreset
	}
	preset := a.Preset(presetKey)
	format := preset.Format
	if f, _ := j.Payload.V["format"].(string); f != "" {
		format = f
	} else if b.Settings.V.ExportFormat != "" && presetKey == "generic" {
		format = b.Settings.V.ExportFormat
	}
	jc.Progress(0.05, "正在计算时间轴")
	tl, err := a.BuildTimeline(&ch, false)
	if err != nil {
		return jobs.Permanent(err)
	}
	allowMissing, _ := j.Payload.V["allowMissing"].(bool)
	if tl.Missing > 0 && !allowMissing {
		return jobs.Permanent(fmt.Errorf("还有 %d 句没有音频", tl.Missing))
	}
	render := &model.Render{ChapterID: ch.ID, BookID: b.ID, Status: "rendering", Preset: preset.Key, Format: format, CreatedBy: j.CreatedBy, JobID: j.ID, Report: model.EmptyMap()}
	a.DB.Create(render)
	dir, err := a.Store.TempDir("render")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	var clips []audio.MixClip
	offset := int64(0)
	// intro / outro templates and AI disclosure
	var label AILabelSettings
	a.Setting("ai_label", &label)
	var introPath, outroPath string
	var introMs, outroMs int64
	labelText := ""
	if label.Explicit || b.Settings.V.AILabel {
		labelText = label.Text
	}
	vars := a.templateVars(&b, &ch)
	// A failed intro/outro fails the render: every episode is meant to carry it. A template that
	// was deleted is skipped.
	tplFail := func(part string, err error) error {
		if err == nil || errors.Is(err, errTemplateGone) {
			return nil
		}
		msg := part + "生成失败：" + err.Error()
		a.DB.Model(render).Updates(map[string]any{"status": "failed", "error": truncateStr(msg, 480)})
		return fmt.Errorf("%s", msg)
	}
	var err1, err2 error
	if tl.Intro && b.Settings.V.IntroTemplateID != "" {
		extra := ""
		if labelText != "" && label.Position == "intro" {
			extra = labelText
		}
		introPath, introMs, err1 = a.renderTemplate(ctx, &b, b.Settings.V.IntroTemplateID, vars, extra, filepath.Join(dir, "intro.wav"))
		if errors.Is(err1, errTemplateGone) && extra != "" {
			introPath, introMs, err1 = a.renderAnnouncement(ctx, &b, extra, filepath.Join(dir, "intro.wav"))
		}
	} else if labelText != "" && label.Position == "intro" {
		introPath, introMs, err1 = a.renderAnnouncement(ctx, &b, labelText, filepath.Join(dir, "intro.wav"))
	}
	if err := tplFail("片头", err1); err != nil {
		return err
	}
	if tl.Outro && b.Settings.V.OutroTemplateID != "" {
		extra := ""
		if labelText != "" && label.Position != "intro" {
			extra = labelText
		}
		outroPath, outroMs, err2 = a.renderTemplate(ctx, &b, b.Settings.V.OutroTemplateID, vars, extra, filepath.Join(dir, "outro.wav"))
		if errors.Is(err2, errTemplateGone) && extra != "" {
			outroPath, outroMs, err2 = a.renderAnnouncement(ctx, &b, extra, filepath.Join(dir, "outro.wav"))
		}
	} else if labelText != "" && label.Position != "intro" {
		outroPath, outroMs, err2 = a.renderAnnouncement(ctx, &b, labelText, filepath.Join(dir, "outro.wav"))
	}
	if err := tplFail("片尾", err2); err != nil {
		return err
	}
	if introPath != "" {
		clips = append(clips, audio.MixClip{Source: introPath, StartMs: 0})
		offset = introMs
	}
	jc.Progress(0.15, "正在混音")
	pathOf := func(assetID string) string {
		p, _, err := a.AssetPath(assetID)
		if err != nil {
			return ""
		}
		return p
	}
	var timings []model.SegmentTiming
	for _, d := range tl.Dialogue {
		if d.Missing || d.AssetID == "" {
			continue
		}
		clips = append(clips, audio.MixClip{Source: pathOf(d.AssetID), StartMs: offset + d.StartMs, DurationMs: d.DurationMs, GainDB: d.VolumeDB, Filter: audio.FilterChain(d.Effects), Voice: true})
		timings = append(timings, model.SegmentTiming{SegmentID: d.SegmentID, CharacterID: d.CharacterID, StartMs: offset + d.StartMs, EndMs: offset + d.StartMs + d.DurationMs, Text: d.Text})
	}
	for _, s := range tl.Sfx {
		clips = append(clips, audio.MixClip{Source: pathOf(s.AssetID), StartMs: offset + s.StartMs, GainDB: s.VolumeDB, Filter: audio.FilterChain(s.Effects)})
	}
	for _, tr := range tl.Tracks {
		if tr.Muted {
			continue
		}
		for _, c := range tr.Resolved {
			src := pathOf(c.AssetID)
			if src == "" {
				continue
			}
			clips = append(clips, audio.MixClip{Source: src, StartMs: offset + c.AbsStartMs, OffsetMs: c.OffsetMs, DurationMs: c.AbsDurMs, Loop: c.Loop || c.Fill,
				GainDB: tr.Volume + c.Volume, FadeInMs: c.FadeInMs, FadeOutMs: c.FadeOutMs, Filter: audio.FilterChain(append(append([]string{}, tr.Effects...), c.Effects...)), Duck: tr.Duck})
		}
	}
	total := offset + tl.DurationMs
	if outroPath != "" {
		clips = append(clips, audio.MixClip{Source: outroPath, StartMs: total})
		total += outroMs
	}
	mixPath := filepath.Join(dir, "mix.wav")
	if _, err := a.FF.Mix(ctx, clips, mixPath, audio.MixOptions{DuckDB: tl.DuckingDb, TotalMs: total}); err != nil {
		a.DB.Model(render).Updates(map[string]any{"status": "failed", "error": truncateStr(err.Error(), 480)})
		return err
	}
	jc.Progress(0.7, "正在标准化响度并编码")
	stats, _ := a.FF.MeasureLoudness(ctx, mixPath, preset.LoudnessI, preset.TruePeak)
	ext := format
	if ext == "" {
		ext = "mp3"
	}
	outPath := filepath.Join(dir, "out."+ext)
	meta := map[string]string{"title": ch.Title, "album": b.Title, "artist": firstNonEmpty(b.Narrator, b.Author), "album_artist": b.Author, "track": fmt.Sprint(ch.Index), "genre": firstNonEmpty(b.Genre, "Audiobook")}
	if label.Implicit {
		meta["comment"] = "AIGC: true; producer: " + label.Producer + "; generated: " + time.Now().Format("2006-01-02")
	}
	cover := ""
	if b.CoverAssetID != "" {
		if p, _, err := a.AssetPath(b.CoverAssetID); err == nil {
			cover = filepath.Join(dir, "cover.jpg")
			if a.FF.ImageToJPEG(ctx, p, cover) != nil {
				cover = ""
			}
		}
	}
	bitrate := preset.Bitrate
	if b.Settings.V.ExportBitrate > 0 && presetKey == "generic" {
		bitrate = b.Settings.V.ExportBitrate
	}
	if err := a.FF.Encode(ctx, mixPath, outPath, audio.EncodeOptions{Format: ext, Bitrate: bitrate, SampleRate: preset.SampleRate, Channels: preset.Channels, TargetI: preset.LoudnessI, TargetTP: preset.TruePeak, Measured: stats, Metadata: meta, CoverPath: cover}); err != nil {
		a.DB.Model(render).Updates(map[string]any{"status": "failed", "error": truncateStr(err.Error(), 480)})
		return err
	}
	jc.Progress(0.88, "正在检测成品技术指标")
	hasBed := false
	for _, tr := range tl.Tracks {
		if tr.Muted {
			continue
		}
		for _, c := range tr.Resolved {
			if c.Fill || c.AbsDurMs > tl.DurationMs*8/10 {
				hasBed = true
			}
		}
	}
	report := a.techReport(ctx, outPath, preset, hasBed || introPath != "" || outroPath != "")
	asset, err := a.SaveAssetFile(ctx, AssetInput{StudioID: b.StudioID, Kind: model.AssetRender, Name: fmt.Sprintf("%s · %s", b.Title, ch.Title), Ext: ext, Origin: "generated", CreatedBy: j.CreatedBy, Meta: map[string]any{"chapterId": ch.ID}}, outPath)
	if err != nil {
		return err
	}
	a.DB.Model(render).Updates(map[string]any{"status": "ready", "asset_id": asset.ID, "duration_ms": asset.DurationMs, "report": model.Map{V: report}, "timings": model.JSON[[]model.SegmentTiming]{V: timings}})
	a.DB.Model(&ch).Updates(map[string]any{"render_id": render.ID, "render_stale": false, "duration_ms": asset.DurationMs})
	a.pruneRenders(ch.ID, 5)
	jc.SetResult(map[string]any{"renderId": render.ID, "assetId": asset.ID, "durationMs": asset.DurationMs, "passed": report["passed"]})
	jc.Progress(1, fmt.Sprintf("成品已生成，时长 %s", fmtDuration(asset.DurationMs)))
	a.emit(b.StudioID, eventChapter(b.ID, ch.ID, "render"))
	a.Activity(b.StudioID, b.ID, j.CreatedBy, "render", "chapter", ch.ID, "合成了「"+ch.Title+"」的成品音频")
	a.AdvanceTaskOnEvent(ch.ID, "post")
	a.FireWebhook(b.StudioID, "chapter.rendered", map[string]any{"bookId": b.ID, "chapterId": ch.ID, "renderId": render.ID, "durationMs": asset.DurationMs})
	return nil
}

func fmtDuration(ms int64) string {
	s := ms / 1000
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s%3600/60, s%60)
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

func (a *App) pruneRenders(chapterID string, keep int) {
	var rs []model.Render
	a.DB.Where("chapter_id = ? AND status = ?", chapterID, "ready").Order("created_at desc").Find(&rs)
	for i, r := range rs {
		if i < keep {
			continue
		}
		var as model.Asset
		if a.DB.First(&as, "id = ?", r.AssetID).Error == nil {
			// keep assets referenced by publications
			var used int64
			a.DB.Model(&model.PublishedEpisode{}).Where("render_id = ?", r.ID).Count(&used)
			if used > 0 {
				continue
			}
			a.DeleteAssetFile(&as)
			a.DB.Delete(&as)
		}
		a.DB.Delete(&r)
	}
}

// techReport checks the deliverable against an export preset.
// techReport checks the deliverable against an export preset. hasBed means continuous music/ambience
// (or intro/outro beds) are present, which makes noise-floor and edge-silence checks meaningless.
func (a *App) techReport(ctx context.Context, path string, p ExportPreset, hasBed bool) map[string]any {
	info, _ := a.FF.Probe(ctx, path)
	loud, _ := a.FF.MeasureLoudness(ctx, path, p.LoudnessI, p.TruePeak)
	pcm, _ := a.FF.DecodePCM(ctx, path, 22050, 1, "")
	var st audio.Stats
	if pcm != nil {
		st = audio.Analyze(pcm)
	}
	var checks []map[string]any
	add := func(key, name string, value, target string, ok bool, tip string) {
		checks = append(checks, map[string]any{"key": key, "name": name, "value": value, "target": target, "ok": ok, "tip": tip})
	}
	if loud != nil {
		ok := math.Abs(loud.InputI-p.LoudnessI) <= 1.5
		add("loudness", "整体响度", fmt.Sprintf("%.1f LUFS", loud.InputI), fmt.Sprintf("%.0f LUFS ±1.5", p.LoudnessI), ok, "重新合成时会自动标准化")
		add("truePeak", "真峰值", fmt.Sprintf("%.1f dBTP", loud.InputTP), fmt.Sprintf("≤ %.1f dBTP", p.TruePeak), loud.InputTP <= p.TruePeak+0.5, "降低过响片段的音量或开启限幅")
	}
	if p.RMSMin != 0 || p.RMSMax != 0 {
		add("rms", "平均电平 (RMS)", fmt.Sprintf("%.1f dB", st.RMSDB), fmt.Sprintf("%.0f ~ %.0f dB", p.RMSMin, p.RMSMax), st.RMSDB >= p.RMSMin-0.5 && st.RMSDB <= p.RMSMax+0.5, "调整响度目标后重新合成")
	}
	if hasBed {
		add("noise", "底噪", "含背景音乐", "不适用", true, "全程铺有背景音乐/环境音，已跳过底噪与首尾静音检测")
	} else {
		add("noise", "底噪", fmt.Sprintf("%.1f dB", st.NoiseFloorDB), fmt.Sprintf("≤ %.0f dB", p.NoiseFloorMax), st.NoiseFloorDB <= p.NoiseFloorMax, "对真人录音使用「降噪」效果")
		add("head", "开头静音", fmt.Sprintf("%d ms", st.HeadSilenceMs), fmt.Sprintf("%d ~ %d ms", p.HeadSilenceMin, p.HeadSilenceMax), st.HeadSilenceMs >= p.HeadSilenceMin && st.HeadSilenceMs <= p.HeadSilenceMax, "在图书设置中调整「章节开头留白」")
		add("tail", "结尾静音", fmt.Sprintf("%d ms", st.TailSilenceMs), fmt.Sprintf("%d ~ %d ms", p.TailSilenceMin, p.TailSilenceMax), st.TailSilenceMs >= p.TailSilenceMin && st.TailSilenceMs <= p.TailSilenceMax, "在图书设置中调整「章节结尾留白」")
	}
	if st.ClippedSamples > 0 {
		add("clipping", "削波/爆音", fmt.Sprintf("%d 处", st.ClippedSamples), "0", st.ClippedSamples < 10, "降低音量或开启限幅")
	}
	if info != nil {
		add("sampleRate", "采样率", fmt.Sprintf("%d Hz", info.SampleRate), fmt.Sprintf("%d Hz", p.SampleRate), p.SampleRate == 0 || info.SampleRate == p.SampleRate, "")
		if p.Bitrate > 0 {
			kb := info.BitRate / 1000
			add("bitrate", "码率", fmt.Sprintf("%d kbps", kb), fmt.Sprintf("%d kbps", p.Bitrate), math.Abs(float64(kb-p.Bitrate)) <= float64(p.Bitrate)/8+4, "")
		}
	}
	passed := true
	for _, c := range checks {
		if !c["ok"].(bool) {
			passed = false
		}
	}
	return map[string]any{"preset": p.Key, "presetName": p.Name, "passed": passed, "checks": checks, "stats": st}
}

func firstErr(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}
