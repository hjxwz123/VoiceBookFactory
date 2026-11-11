package core

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"voicebook/internal/audio"
	"voicebook/internal/export"
	"voicebook/internal/jobs"
	"voicebook/internal/model"
)

type ExportInput struct {
	ChapterIDs []string `json:"chapterIds"`
	Format     string   `json:"format"`    // mp3 | wav | m4a | m4b
	Packaging  string   `json:"packaging"` // zip | single
	Bitrate    int      `json:"bitrate"`
	Preset     string   `json:"preset"`
	Subtitles  []string `json:"subtitles"` // srt | lrc | json
	Stems      bool     `json:"stems"`
	Script     bool     `json:"script"`   // docx script
	Lines      bool     `json:"lines"`    // xlsx lines per character
	Timeline   bool     `json:"timeline"` // json timeline project
	Naming     string   `json:"naming"`   // {index} {title} {book}
}

func (a *App) CreateExport(x *Access, in ExportInput) (*model.Export, error) {
	if err := need(x, PermExport); err != nil {
		return nil, err
	}
	var chs []model.Chapter
	q := a.DB.Where("book_id = ? AND included = ?", x.Book.ID, true).Order("\"index\" asc")
	if len(in.ChapterIDs) > 0 {
		q = q.Where("id IN ?", in.ChapterIDs)
	}
	q.Find(&chs)
	if len(chs) == 0 {
		return nil, Bad("没有可导出的章节")
	}
	var unscripted []string
	missing := 0
	for i := range chs {
		if chs[i].CurrentScriptID == "" {
			unscripted = append(unscripted, chs[i].Title)
			continue
		}
		if tl, err := a.BuildTimeline(&chs[i], false); err == nil {
			missing += tl.Missing
		}
	}
	if len(unscripted) > 0 {
		return nil, Bad("以下章节还没有画本：%s", strings.Join(unscripted[:min(3, len(unscripted))], "、"))
	}
	if missing > 0 {
		return nil, &Error{Status: 409, Code: "missing_audio", Msg: fmt.Sprintf("所选章节还有 %d 句没有音频，请先完成配音", missing)}
	}
	if in.Format == "" {
		in.Format = firstNonEmpty(x.Book.Settings.V.ExportFormat, "mp3")
	}
	plan := a.StudioPlan(x.StudioID)
	if fs := plan.ExportFormats.V; len(fs) > 0 && !slices.Contains(fs, in.Format) {
		return nil, &Error{Status: 403, Code: "plan_required", Msg: fmt.Sprintf("「%s」可导出 %s 格式，导出 %s 需升级会员", plan.Name, strings.ToUpper(strings.Join(fs, " / ")), strings.ToUpper(in.Format)), Data: map[string]any{"kind": "exportFormat"}}
	}
	if in.Stems && !a.HasFeature(x.StudioID, model.FeatStems) {
		return nil, &Error{Status: 403, Code: "plan_required", Msg: "「" + plan.Name + "」不包含分轨导出，升级会员后可用", Data: map[string]any{"kind": "stems"}}
	}
	if in.Format == "m4b" {
		in.Packaging = "single"
	}
	if in.Packaging == "" {
		in.Packaging = "zip"
	}
	if in.Naming == "" {
		in.Naming = firstNonEmpty(x.Book.Settings.V.NamingRule, "{index}_{title}")
	}
	var ids []string
	for _, c := range chs {
		ids = append(ids, c.ID)
	}
	opts := map[string]any{}
	b, _ := json.Marshal(in)
	json.Unmarshal(b, &opts)
	e := &model.Export{StudioID: x.StudioID, BookID: x.Book.ID, ChapterIDs: model.Strings(ids...), Format: in.Format, Options: model.Map{V: opts}, Status: "running", CreatedBy: x.User.ID}
	if err := a.DB.Create(e).Error; err != nil {
		return nil, err
	}
	j := &model.Job{Type: "export", StudioID: x.StudioID, BookID: x.Book.ID, RefType: "export", RefID: e.ID, Title: fmt.Sprintf("导出《%s》（%d 章）", x.Book.Title, len(ids)), CreatedBy: x.User.ID, MaxAttempts: 1}
	if err := a.Q.Enqueue(j); err != nil {
		return nil, err
	}
	a.DB.Model(e).Update("job_id", j.ID)
	return e, nil
}

func nameFor(rule string, b *model.Book, ch *model.Chapter, ext string) string {
	r := strings.NewReplacer("{index}", fmt.Sprintf("%03d", ch.Index), "{title}", ch.Title, "{book}", b.Title, "{volume}", ch.Volume)
	return export.SafeName(r.Replace(rule)) + "." + ext
}

func (a *App) jobExport(ctx context.Context, jc *jobs.Context) error {
	j := jc.Job
	var e model.Export
	if err := a.DB.First(&e, "id = ?", j.RefID).Error; err != nil {
		return jobs.Permanent(err)
	}
	fail := func(err error) error {
		a.DB.Model(&e).Updates(map[string]any{"status": "failed", "error": truncateStr(err.Error(), 480)})
		return jobs.Permanent(err)
	}
	var in ExportInput
	raw, _ := json.Marshal(e.Options.V)
	json.Unmarshal(raw, &in)
	var b model.Book
	a.DB.First(&b, "id = ?", e.BookID)
	var chs []model.Chapter
	a.DB.Where("id IN ?", e.ChapterIDs.V).Order("\"index\" asc").Find(&chs)
	dir, err := a.Store.TempDir("export")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	chars := a.charMap(b.ID)
	n := float64(len(chs))
	var entries []export.ZipEntry
	var renderPaths []string
	var bookCues []export.Cue
	offset := int64(0)
	preset := a.Preset(firstNonEmpty(in.Preset, b.Settings.V.LoudnessPreset))
	for i := range chs {
		ch := &chs[i]
		base := float64(i) / n
		// render missing or stale chapters first (credits for TTS were spent earlier; rendering is free)
		if ch.RenderID == "" || ch.RenderStale {
			sub := &model.Job{ID: j.ID, StudioID: b.StudioID, BookID: b.ID, ChapterID: ch.ID, CreatedBy: j.CreatedBy, Payload: model.Map{V: map[string]any{"preset": preset.Key}}}
			if err := a.renderChapter(ctx, sub, &subReporter{parent: jc, base: base, span: 0.6 / n, prefix: ch.Title}); err != nil {
				return fail(fmt.Errorf("合成「%s」失败：%v", ch.Title, err))
			}
			a.DB.First(ch, "id = ?", ch.ID)
		}
		jc.Progress(base+0.6/n, "正在导出 "+ch.Title)
		var r model.Render
		if err := a.DB.First(&r, "id = ?", ch.RenderID).Error; err != nil {
			return fail(fmt.Errorf("「%s」没有成品", ch.Title))
		}
		src, _, err := a.AssetPath(r.AssetID)
		if err != nil {
			return fail(err)
		}
		// transcode to the requested format when needed
		out := src
		if in.Format != "m4b" && (in.Format != r.Format || (in.Bitrate > 0 && in.Bitrate != preset.Bitrate)) {
			out = filepath.Join(dir, fmt.Sprintf("ch%03d.%s", ch.Index, in.Format))
			if err := a.FF.Encode(ctx, src, out, audio.EncodeOptions{Format: in.Format, Bitrate: in.Bitrate, SampleRate: preset.SampleRate, Metadata: map[string]string{"title": ch.Title, "album": b.Title, "artist": firstNonEmpty(b.Narrator, b.Author), "track": fmt.Sprint(ch.Index)}}); err != nil {
				return fail(err)
			}
		}
		renderPaths = append(renderPaths, out)
		ext := in.Format
		if ext == "m4b" {
			ext = r.Format
		}
		if in.Packaging == "zip" {
			entries = append(entries, export.ZipEntry{Name: "音频/" + nameFor(in.Naming, &b, ch, ext), Path: out})
		}
		var cues []export.Cue
		for _, t := range r.Timings.V {
			sp := ""
			if c := chars[t.CharacterID]; c != nil && c.Kind != model.CharNarrator {
				sp = c.Name
			}
			cues = append(cues, export.Cue{StartMs: t.StartMs, EndMs: t.EndMs, Speaker: sp, Text: t.Text})
		}
		for _, sub := range in.Subtitles {
			stem := strings.TrimSuffix(nameFor(in.Naming, &b, ch, "x"), ".x")
			switch sub {
			case "srt":
				entries = append(entries, export.ZipEntry{Name: "字幕/" + stem + ".srt", Data: []byte(export.SRT(cues, true))})
			case "lrc":
				entries = append(entries, export.ZipEntry{Name: "字幕/" + stem + ".lrc", Data: []byte(export.LRC(ch.Title, b.Author, cues))})
			case "json":
				data, _ := json.MarshalIndent(r.Timings.V, "", "  ")
				entries = append(entries, export.ZipEntry{Name: "字幕/" + stem + ".json", Data: data})
			}
		}
		bookCues = append(bookCues, export.Cue{StartMs: offset, EndMs: offset + r.DurationMs, Text: ch.Title})
		offset += r.DurationMs
		if in.Stems {
			stems, err := a.renderStems(ctx, ch, dir)
			if err != nil {
				return fail(err)
			}
			for name, p := range stems {
				entries = append(entries, export.ZipEntry{Name: fmt.Sprintf("分轨/%03d_%s/%s.wav", ch.Index, export.SafeName(ch.Title), export.SafeName(name)), Path: p})
			}
		}
		if in.Timeline {
			tl, _ := a.BuildTimeline(ch, false)
			data, _ := json.MarshalIndent(tl, "", "  ")
			folder := fmt.Sprintf("工程/%03d_%s", ch.Index, export.SafeName(ch.Title))
			entries = append(entries, export.ZipEntry{Name: folder + "/timeline.json", Data: data})
			otioDoc, media := a.otioFor(&b, ch, tl, chars)
			od, _ := json.MarshalIndent(otioDoc, "", "  ")
			entries = append(entries, export.ZipEntry{Name: folder + "/" + export.SafeName(ch.Title) + ".otio", Data: od})
			for rel, path := range media {
				entries = append(entries, export.ZipEntry{Name: folder + "/" + rel, Path: path})
			}
		}
	}
	jc.Progress(0.85, "正在打包")
	if in.Script || in.Lines {
		docs, err := a.writeScripts(&b, chs, dir, in.Script, in.Lines)
		if err != nil {
			return fail(err)
		}
		entries = append(entries, docs...)
	}
	var finalPath, fileName string
	switch {
	case in.Format == "m4b" || in.Packaging == "single":
		wavs := []string{}
		for i, p := range renderPaths {
			w := filepath.Join(dir, fmt.Sprintf("cat%03d.wav", i))
			if err := a.FF.ToWAV(ctx, p, w, 44100, 2, ""); err != nil {
				return fail(err)
			}
			wavs = append(wavs, w)
		}
		cat := filepath.Join(dir, "book.wav")
		if err := a.FF.ConcatWAV(ctx, wavs, cat); err != nil {
			return fail(err)
		}
		ext := in.Format
		meta := filepath.Join(dir, "chapters.txt")
		os.WriteFile(meta, []byte(export.FFMetadata(b.Title, firstNonEmpty(b.Author, b.Narrator), bookCues)), 0o644)
		cover := ""
		if b.CoverAssetID != "" {
			if p, _, err := a.AssetPath(b.CoverAssetID); err == nil {
				cover = filepath.Join(dir, "cover.jpg")
				if a.FF.ImageToJPEG(ctx, p, cover) != nil {
					cover = ""
				}
			}
		}
		chapterMeta := ""
		if ext == "m4b" || ext == "m4a" {
			chapterMeta = meta
		}
		finalPath = filepath.Join(dir, "book."+ext)
		if err := a.FF.Encode(ctx, cat, finalPath, audio.EncodeOptions{Format: ext, Bitrate: firstNonZeroInt(in.Bitrate, preset.Bitrate, 128), SampleRate: preset.SampleRate, CoverPath: cover, ChapterMeta: chapterMeta,
			Metadata: map[string]string{"title": b.Title, "album": b.Title, "artist": firstNonEmpty(b.Narrator, b.Author), "album_artist": b.Author, "genre": "Audiobook"}}); err != nil {
			return fail(err)
		}
		fileName = export.SafeName(b.Title) + "." + ext
		if len(entries) > 0 {
			entries = append(entries, export.ZipEntry{Name: fileName, Path: finalPath})
			zipPath := filepath.Join(dir, "export.zip")
			if err := export.WriteZip(zipPath, entries); err != nil {
				return fail(err)
			}
			finalPath, fileName = zipPath, export.SafeName(b.Title)+".zip"
		}
	default:
		finalPath = filepath.Join(dir, "export.zip")
		if err := export.WriteZip(finalPath, entries); err != nil {
			return fail(err)
		}
		fileName = export.SafeName(b.Title) + ".zip"
	}
	ext := strings.TrimPrefix(filepath.Ext(fileName), ".")
	asset, err := a.SaveAssetFile(ctx, AssetInput{StudioID: b.StudioID, Kind: model.AssetExport, Name: fileName, Ext: ext, Origin: "generated", CreatedBy: j.CreatedBy, NoPeaks: true, NoProbe: ext == "zip"}, finalPath)
	if err != nil {
		return fail(err)
	}
	a.DB.Model(&e).Updates(map[string]any{"status": "ready", "asset_id": asset.ID, "size": asset.Size, "file_name": fileName})
	jc.SetResult(map[string]any{"exportId": e.ID, "assetId": asset.ID})
	jc.Progress(1, "导出完成："+fileName)
	a.FireWebhook(b.StudioID, "export.completed", map[string]any{"bookId": b.ID, "exportId": e.ID, "fileName": fileName, "size": asset.Size})
	return nil
}

func firstNonZeroInt(vals ...int) int {
	for _, v := range vals {
		if v != 0 {
			return v
		}
	}
	return 0
}

// renderStems renders one WAV per character plus the music/effects tracks.
func (a *App) renderStems(ctx context.Context, ch *model.Chapter, dir string) (map[string]string, error) {
	tl, err := a.BuildTimeline(ch, false)
	if err != nil {
		return nil, err
	}
	chars := a.charMap(ch.BookID)
	groups := map[string][]audio.MixClip{}
	for _, d := range tl.Dialogue {
		if d.Missing {
			continue
		}
		p, _, err := a.AssetPath(d.AssetID)
		if err != nil {
			continue
		}
		name := "未知角色"
		if c := chars[d.CharacterID]; c != nil {
			name = c.Name
		}
		groups[name] = append(groups[name], audio.MixClip{Source: p, StartMs: d.StartMs, DurationMs: d.DurationMs, GainDB: d.VolumeDB, Filter: audio.FilterChain(d.Effects)})
	}
	for _, s := range tl.Sfx {
		if p, _, err := a.AssetPath(s.AssetID); err == nil {
			groups["音效"] = append(groups["音效"], audio.MixClip{Source: p, StartMs: s.StartMs})
		}
	}
	for _, tr := range tl.Tracks {
		for _, c := range tr.Resolved {
			if p, _, err := a.AssetPath(c.AssetID); err == nil {
				groups[tr.Name] = append(groups[tr.Name], audio.MixClip{Source: p, StartMs: c.AbsStartMs, OffsetMs: c.OffsetMs, DurationMs: c.AbsDurMs, Loop: c.Loop || c.Fill, GainDB: tr.Volume + c.Volume, FadeInMs: c.FadeInMs, FadeOutMs: c.FadeOutMs})
			}
		}
	}
	out := map[string]string{}
	for name, clips := range groups {
		p := filepath.Join(dir, fmt.Sprintf("stem-%s-%d.wav", ch.ID[:8], len(out)))
		if _, err := a.FF.Mix(ctx, clips, p, audio.MixOptions{TotalMs: tl.DurationMs}); err != nil {
			return nil, err
		}
		out[name] = p
	}
	return out, nil
}

// writeScripts writes the colored Word script and the per-character lines workbook.
func (a *App) writeScripts(b *model.Book, chs []model.Chapter, dir string, script, lines bool) ([]export.ZipEntry, error) {
	chars := a.charMap(b.ID)
	var entries []export.ZipEntry
	var doc []export.DocLine
	byChar := map[string][][]any{}
	order := []string{}
	for _, ch := range chs {
		segs := a.scriptSegments(ch.CurrentScriptID)
		doc = append(doc, export.DocLine{Heading: true, Text: ch.Title})
		for i, s := range segs {
			if s.Removed {
				continue
			}
			c := chars[s.CharacterID]
			name, color := "", ""
			if c != nil {
				name, color = c.Name, c.Color
			}
			if s.Type == model.SegSfx {
				doc = append(doc, export.DocLine{Speaker: "音效", Color: "#059669", Text: s.Text})
				continue
			}
			var notes []string
			if s.Emotion != "" && s.Emotion != "neutral" {
				notes = append(notes, emotionName(s.Emotion))
			}
			if s.Instruction != "" {
				notes = append(notes, s.Instruction)
			}
			shown := CueDisplayText(s.Text, s.Cues.V)
			doc = append(doc, export.DocLine{Speaker: name, Color: color, Text: shown, Note: strings.Join(notes, "；")})
			if c != nil && c.Kind != model.CharNarrator {
				if _, ok := byChar[c.Name]; !ok {
					order = append(order, c.Name)
				}
				prev, next := "", ""
				if i > 0 {
					prev = segs[i-1].Text
				}
				if i+1 < len(segs) {
					next = segs[i+1].Text
				}
				byChar[c.Name] = append(byChar[c.Name], []any{ch.Index, ch.Title, shown, emotionName(s.Emotion), s.Instruction, prev, next, s.Note})
			}
		}
	}
	if script {
		p := filepath.Join(dir, "script.docx")
		if err := export.WriteDocx(p, b.Title+" · 演播稿", doc); err != nil {
			return nil, err
		}
		entries = append(entries, export.ZipEntry{Name: "文稿/" + export.SafeName(b.Title) + "_演播稿.docx", Path: p})
	}
	if lines && len(order) > 0 {
		var sheets []export.Sheet
		for _, name := range order {
			sheets = append(sheets, export.Sheet{Name: name, Header: []string{"章节", "章节名", "台词", "情绪", "表演指导", "上一句", "下一句", "备注"}, Rows: byChar[name], Widths: []float64{6, 16, 50, 10, 24, 30, 30, 20}})
		}
		p := filepath.Join(dir, "lines.xlsx")
		if err := export.WriteXLSX(p, sheets); err != nil {
			return nil, err
		}
		entries = append(entries, export.ZipEntry{Name: "文稿/" + export.SafeName(b.Title) + "_分角色台词本.xlsx", Path: p})
	}
	return entries, nil
}

var emotionNames = map[string]string{"neutral": "平静", "happy": "开心", "sad": "悲伤", "angry": "愤怒", "fearful": "恐惧", "surprised": "惊讶", "disgusted": "厌恶", "gentle": "温柔", "nervous": "紧张", "tired": "疲惫", "whisper": "低语", "shout": "喊叫"}

func emotionName(e string) string {
	if n, ok := emotionNames[e]; ok {
		return n
	}
	return e
}

// ScriptDocument exports a single chapter script directly (used by the editor's download button).
func (a *App) ScriptDocument(x *Access, chapterIDs []string, kind string) (string, string, error) {
	var chs []model.Chapter
	a.DB.Where("book_id = ? AND id IN ? AND current_script_id <> ''", x.Book.ID, chapterIDs).Order("\"index\" asc").Find(&chs)
	if len(chs) == 0 {
		return "", "", Bad("所选章节还没有画本")
	}
	dir, err := a.Store.TempDir("doc")
	if err != nil {
		return "", "", err
	}
	entries, err := a.writeScripts(x.Book, chs, dir, kind == "docx", kind == "xlsx")
	if err != nil || len(entries) == 0 {
		os.RemoveAll(dir)
		return "", "", firstErr(err, Bad("没有可导出的角色台词"))
	}
	ext := kind
	asset, err := a.SaveAssetFile(context.Background(), AssetInput{StudioID: x.StudioID, Kind: model.AssetExport, Name: filepath.Base(entries[0].Name), Ext: ext, Origin: "generated", CreatedBy: x.User.ID, NoProbe: true}, entries[0].Path)
	os.RemoveAll(dir)
	if err != nil {
		return "", "", err
	}
	return a.Store.SignedPath(asset.StorageKey, 2*time.Hour, filepath.Base(entries[0].Name)), asset.Name, nil
}

// otioFor converts a chapter timeline into OpenTimelineIO tracks (one per character + music/effects).
func (a *App) otioFor(b *model.Book, ch *model.Chapter, tl *Timeline, chars map[string]*model.Character) (map[string]any, map[string]string) {
	media := map[string]string{}
	ref := func(assetID string) (string, int64) {
		p, as, err := a.AssetPath(assetID)
		if err != nil {
			return "", 0
		}
		rel := "media/" + as.ID + filepath.Ext(p)
		media[rel] = p
		return rel, as.DurationMs
	}
	byChar := map[string]*export.OTIOTrack{}
	var order []string
	for _, d := range tl.Dialogue {
		if d.Missing {
			continue
		}
		name := "未知角色"
		if c := chars[d.CharacterID]; c != nil {
			name = c.Name
		}
		if byChar[name] == nil {
			byChar[name] = &export.OTIOTrack{Name: name}
			order = append(order, name)
		}
		rel, full := ref(d.AssetID)
		byChar[name].Clips = append(byChar[name].Clips, export.OTIOClip{Name: clip(d.Text, 20), StartMs: d.StartMs, DurMs: d.DurationMs, Media: rel, MediaMs: full})
	}
	var tracks []export.OTIOTrack
	for _, n := range order {
		tracks = append(tracks, *byChar[n])
	}
	if len(tl.Sfx) > 0 {
		t := export.OTIOTrack{Name: "音效提示"}
		for _, s := range tl.Sfx {
			rel, full := ref(s.AssetID)
			t.Clips = append(t.Clips, export.OTIOClip{Name: s.Text, StartMs: s.StartMs, DurMs: s.DurationMs, Media: rel, MediaMs: full})
		}
		tracks = append(tracks, t)
	}
	for _, mt := range tl.Tracks {
		if len(mt.Resolved) == 0 {
			continue
		}
		t := export.OTIOTrack{Name: mt.Name}
		for _, c := range mt.Resolved {
			rel, full := ref(c.AssetID)
			dur := c.AbsDurMs
			if !c.Loop && !c.Fill && full > 0 && dur > full-c.OffsetMs {
				dur = full - c.OffsetMs
			}
			t.Clips = append(t.Clips, export.OTIOClip{Name: c.Name, StartMs: c.AbsStartMs, OffsetMs: c.OffsetMs, DurMs: dur, Media: rel, MediaMs: full})
		}
		tracks = append(tracks, t)
	}
	return export.OTIO(b.Title+" · "+ch.Title, tracks), media
}
