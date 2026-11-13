package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"voicebook/internal/ai/asr"
	"voicebook/internal/align"
	"voicebook/internal/audio"
	"voicebook/internal/jobs"
	"voicebook/internal/model"
)

// SaveRecordedTake stores a human recording (browser recording or upload) for a segment.
func (a *App) SaveRecordedTake(ctx context.Context, x *Access, s *model.Segment, srcPath, origin string, denoise bool) (*model.Take, map[string]any, error) {
	if err := need(x, PermRecord); err != nil {
		if err2 := need(x, PermPost); err2 != nil {
			return nil, nil, err
		}
	}
	if !a.CanVoiceCharacter(x, s.CharacterID) {
		return nil, nil, Forbidden("你只能为分配给自己的角色录音")
	}
	filter := "highpass=f=60"
	if denoise {
		filter += ",afftdn=nf=-28"
	}
	pcm, err := a.FF.DecodePCM(ctx, srcPath, audio.SampleRate, 1, filter)
	if err != nil {
		return nil, nil, Bad("无法读取录音：%v", err)
	}
	stats := audio.Analyze(pcm)
	pcm = audio.TrimSilence(pcm, -46, 60)
	audio.NormalizeRMS(pcm, -20, -2)
	dir, err := a.Store.TempDir("rec")
	if err != nil {
		return nil, nil, err
	}
	defer os.RemoveAll(dir)
	out := filepath.Join(dir, "take.wav")
	if err := audio.WriteWAVFile(out, pcm); err != nil {
		return nil, nil, err
	}
	asset, err := a.SaveAssetFile(ctx, AssetInput{StudioID: x.StudioID, Kind: model.AssetTake, Name: "录音", Ext: "wav", Origin: "upload", CreatedBy: x.User.ID, Meta: map[string]any{"segmentId": s.ID}}, out)
	if err != nil {
		return nil, nil, err
	}
	t := model.Take{SegmentID: s.ID, ChapterID: s.ChapterID, Origin: origin, AssetID: asset.ID, DurationMs: int(asset.DurationMs), Status: model.AudioReady, CreatedBy: x.User.ID, Params: model.EmptyMap(), QCDetail: model.Map{V: map[string]any{"rmsDb": stats.RMSDB, "peakDb": stats.PeakDB, "noiseDb": stats.NoiseFloorDB, "clipped": stats.ClippedSamples}}}
	if err := a.DB.Create(&t).Error; err != nil {
		return nil, nil, err
	}
	a.selectTake(s, &t, s.ContentHash)
	hints := map[string]any{"durationMs": asset.DurationMs, "rmsDb": stats.RMSDB, "noiseDb": stats.NoiseFloorDB, "clipped": stats.ClippedSamples, "tips": recordingTips(stats)}
	// quick text check when ASR is configured
	if prov, _, err := a.ASRFor(x.StudioID); err == nil {
		if res, err := prov.Transcribe(ctx, out, x.Book.Language); err == nil {
			_, score := align.Compare(s.Text, res.Text)
			hints["heard"], hints["match"] = res.Text, score
			if score < 0.8 {
				hints["tips"] = append(hints["tips"].([]string), fmt.Sprintf("识别内容与台词相似度 %.0f%%，请确认是否读错或漏读", score*100))
			}
		}
	}
	a.emit(x.StudioID, eventSegments(s.BookID, s.ChapterID, a.SegmentViews([]model.Segment{*s})))
	a.checkRecordTaskDone(s)
	return &t, hints, nil
}

func recordingTips(st audio.Stats) []string {
	tips := []string{}
	if st.RMSDB < -34 {
		tips = append(tips, "音量偏小，请靠近麦克风")
	}
	if st.ClippedSamples > 20 {
		tips = append(tips, "出现爆音，请降低输入音量或离麦克风稍远")
	}
	if st.NoiseFloorDB > -45 {
		tips = append(tips, "底噪偏大，建议开启降噪或换更安静的环境")
	}
	return tips
}

// checkRecordTaskDone notifies the recorder when all lines of their character in a chapter are recorded.
func (a *App) checkRecordTaskDone(s *model.Segment) {
	var left int64
	a.DB.Model(&model.Segment{}).Where("script_id = ? AND character_id = ? AND removed = ? AND audio_status <> ?", s.ScriptID, s.CharacterID, false, model.AudioReady).Count(&left)
	if left == 0 {
		a.AdvanceTaskOnEvent(s.ChapterID, "record:"+s.CharacterID)
	}
}

// ---------- smart alignment ----------

func (a *App) CreateRecording(x *Access, ch *model.Chapter, assetIDs []string, mode, characterID string) (*model.Recording, error) {
	if err := need(x, PermAlign); err != nil {
		return nil, err
	}
	if err := a.RequireFeature(x.StudioID, model.FeatAlignment, "智能对轨"); err != nil {
		return nil, err
	}
	if ch.CurrentScriptID == "" {
		return nil, Bad("请先生成画本，再进行对轨")
	}
	if _, _, err := a.ASRFor(x.StudioID); err != nil {
		return nil, err
	}
	if len(assetIDs) == 0 {
		return nil, Bad("请上传录音文件")
	}
	if mode != "character" {
		mode = "full"
	} else if characterID == "" {
		return nil, Bad("单角色模式需要选择角色")
	} else if !a.CanVoiceCharacter(x, characterID) {
		return nil, Forbidden("你只能为分配给自己的角色对轨")
	}
	var dur int64
	for _, id := range assetIDs {
		var as model.Asset
		if a.DB.First(&as, "id = ? AND studio_id = ?", id, x.StudioID).Error != nil {
			return nil, NotFound("录音文件")
		}
		dur += as.DurationMs
	}
	est := a.Estimate(FeatAlign, "", float64(dur)/1000)
	if b := a.Balance(x.StudioID); b.Available < est {
		return nil, PaymentRequired(fmt.Sprintf("积分不足：对轨预计需要 %d 积分", est), map[string]any{"need": est, "available": b.Available})
	}
	r := &model.Recording{ChapterID: ch.ID, BookID: ch.BookID, UploaderID: x.User.ID, AssetIDs: model.Strings(assetIDs...), Mode: mode, CharacterID: characterID, Status: "processing", DurationMs: dur}
	r.Result.V = model.AlignResult{Matches: []model.AlignMatch{}, Extras: []model.AlignSpan{}, Missing: []string{}, Stats: model.EmptyMap()}
	if err := a.DB.Create(r).Error; err != nil {
		return nil, err
	}
	j := &model.Job{Type: "align", StudioID: x.StudioID, BookID: ch.BookID, ChapterID: ch.ID, RefType: "recording", RefID: r.ID, Title: "智能对轨 · " + ch.Title, CreatedBy: x.User.ID, MaxAttempts: 2}
	if err := a.Q.Enqueue(j); err != nil {
		return nil, err
	}
	a.DB.Model(r).Update("job_id", j.ID)
	return r, nil
}

// transcribeLong splits long audio into chunks the ASR service accepts and offsets timestamps.
func (a *App) transcribeLong(ctx context.Context, prov asr.Provider, path, lang string, progress func(float64)) ([]align.Word, string, error) {
	info, err := a.FF.Probe(ctx, path)
	if err != nil {
		return nil, "", err
	}
	dir, err := a.Store.TempDir("asr")
	if err != nil {
		return nil, "", err
	}
	defer os.RemoveAll(dir)
	const chunk = int64(9 * 60 * 1000)
	var words []align.Word
	var full strings.Builder
	n := int((info.DurationMs + chunk - 1) / chunk)
	for i := 0; i < n; i++ {
		start := int64(i) * chunk
		end := min(start+chunk, info.DurationMs)
		out := filepath.Join(dir, fmt.Sprintf("c%03d.mp3", i))
		if _, err := a.FF.EncodeChunk(ctx, path, out, start, end); err != nil {
			return nil, "", err
		}
		res, err := prov.Transcribe(ctx, out, lang)
		if err != nil {
			return nil, "", withDetail("语音识别失败", err)
		}
		for _, w := range res.Words {
			words = append(words, align.Word{Text: w.Text, StartMs: w.StartMs + start, EndMs: w.EndMs + start})
		}
		full.WriteString(res.Text)
		if progress != nil {
			progress(float64(i+1) / float64(n))
		}
	}
	return words, full.String(), nil
}

func (a *App) jobAlign(ctx context.Context, jc *jobs.Context) error {
	j := jc.Job
	var r model.Recording
	if err := a.DB.First(&r, "id = ?", j.RefID).Error; err != nil {
		return jobs.Permanent(err)
	}
	fail := func(err error) error {
		a.DB.Model(&r).Updates(map[string]any{"status": "failed", "error": truncateStr(err.Error(), 480)})
		return jobs.Permanent(err)
	}
	var ch model.Chapter
	a.DB.First(&ch, "id = ?", r.ChapterID)
	var b model.Book
	a.DB.First(&b, "id = ?", r.BookID)
	prov, acc, err := a.ASRFor(b.StudioID)
	if err != nil {
		return fail(err)
	}
	dir, err := a.Store.TempDir("align")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	jc.Progress(0.05, "正在合并录音")
	var inputs []string
	for i, id := range r.AssetIDs.V {
		p, _, err := a.AssetPath(id)
		if err != nil {
			return fail(err)
		}
		out := filepath.Join(dir, fmt.Sprintf("in%02d.wav", i))
		if err := a.FF.ToWAV(ctx, p, out, audio.SampleRate, 1, ""); err != nil {
			return fail(err)
		}
		inputs = append(inputs, out)
	}
	merged := filepath.Join(dir, "merged.wav")
	if len(inputs) == 1 {
		merged = inputs[0]
	} else if err := a.FF.ConcatWAV(ctx, inputs, merged); err != nil {
		return fail(err)
	}
	if as, err := a.SaveAssetFile(ctx, AssetInput{StudioID: b.StudioID, Kind: model.AssetRecording, Name: "对轨录音（合并）", Ext: "wav", Origin: "upload", CreatedBy: r.UploaderID, NoPeaks: false}, merged); err == nil {
		a.DB.Model(&r).Update("merged_asset", as.ID)
		merged = a.Store.Path(as.StorageKey)
	}
	jc.Progress(0.15, "正在识别语音")
	words, _, err := a.transcribeLong(ctx, prov, merged, b.Language, func(p float64) { jc.Progress(0.15+p*0.55, "正在识别语音") })
	if err != nil {
		return fail(err)
	}
	if _, err := a.ChargeCredits(Charge{StudioID: b.StudioID, UserID: r.UploaderID, BookID: b.ID, ChapterID: ch.ID, JobID: j.ID, Feature: FeatAlign, Quantity: float64(r.DurationMs) / 1000, Unit: "seconds", Provider: acc.Provider, AccountID: acc.ID}); err != nil {
		return fail(err)
	}
	jc.Progress(0.72, "正在把录音匹配到画本")
	q := a.DB.Where("script_id = ? AND removed = ? AND type NOT IN ?", ch.CurrentScriptID, false, []string{model.SegSfx, model.SegPause})
	if r.Mode == "character" {
		q = q.Where("character_id = ?", r.CharacterID)
	}
	var segs []model.Segment
	q.Order("seq asc").Find(&segs)
	var st []align.SegText
	byID := map[string]*model.Segment{}
	for i := range segs {
		st = append(st, align.SegText{ID: segs[i].ID, Text: segs[i].Text})
		byID[segs[i].ID] = &segs[i]
	}
	res := align.Align(st, words)
	out := model.AlignResult{Matches: []model.AlignMatch{}, Extras: []model.AlignSpan{}, Missing: res.Missing, Stats: model.EmptyMap()}
	if out.Missing == nil {
		out.Missing = []string{}
	}
	for i, m := range res.Matches {
		jc.Progress(0.75+0.2*float64(i)/float64(max(1, len(res.Matches))), "正在切片")
		take, err := a.sliceTake(ctx, &b, byID[m.SegmentID], merged, m.StartMs, m.EndMs, r.UploaderID, r.ID)
		am := model.AlignMatch{SegmentID: m.SegmentID, StartMs: m.StartMs, EndMs: m.EndMs, Confidence: m.Confidence, Alternatives: []model.AlignSpan{}}
		for _, alt := range m.Alternatives {
			am.Alternatives = append(am.Alternatives, model.AlignSpan{StartMs: alt.StartMs, EndMs: alt.EndMs, Text: alt.Text, Similarity: alt.Similarity})
		}
		if err == nil {
			am.TakeID = take.ID
		}
		out.Matches = append(out.Matches, am)
	}
	for _, e := range res.Extras {
		out.Extras = append(out.Extras, model.AlignSpan{StartMs: e.StartMs, EndMs: e.EndMs, Text: e.Text})
	}
	out.Stats.V = map[string]any{"segments": len(segs), "matched": len(out.Matches), "missing": len(out.Missing), "extras": len(out.Extras), "words": len(words)}
	a.DB.Model(&r).Updates(map[string]any{"status": "done", "result": model.JSON[model.AlignResult]{V: out}})
	jc.SetResult(map[string]any{"recordingId": r.ID, "matched": len(out.Matches), "missing": len(out.Missing)})
	jc.Progress(1, fmt.Sprintf("对轨完成：匹配 %d 句，漏录 %d 句，多余片段 %d 段", len(out.Matches), len(out.Missing), len(out.Extras)))
	a.emit(b.StudioID, eventChapter(b.ID, ch.ID, "audio"))
	return nil
}

func (a *App) sliceTake(ctx context.Context, b *model.Book, s *model.Segment, src string, startMs, endMs int64, userID, recordingID string) (*model.Take, error) {
	if s == nil {
		return nil, Bad("片段不存在")
	}
	dir, err := a.Store.TempDir("slice")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	out := filepath.Join(dir, "slice.wav")
	if err := a.FF.Slice(ctx, src, out, startMs, endMs); err != nil {
		return nil, err
	}
	pcm, err := audio.ReadWAVFile(out)
	if err != nil {
		return nil, err
	}
	audio.NormalizeRMS(pcm, -20, -2)
	audio.WriteWAVFile(out, pcm)
	asset, err := a.SaveAssetFile(ctx, AssetInput{StudioID: b.StudioID, Kind: model.AssetTake, Name: "对轨切片", Ext: "wav", Origin: "upload", CreatedBy: userID, Meta: map[string]any{"segmentId": s.ID, "recordingId": recordingID, "startMs": startMs, "endMs": endMs}}, out)
	if err != nil {
		return nil, err
	}
	t := model.Take{SegmentID: s.ID, ChapterID: s.ChapterID, Origin: model.TakeAlign, AssetID: asset.ID, DurationMs: int(asset.DurationMs), Status: model.AudioReady, CreatedBy: userID,
		Params: model.Map{V: map[string]any{"recordingId": recordingID, "startMs": startMs, "endMs": endMs}}, QCDetail: model.EmptyMap()}
	if err := a.DB.Create(&t).Error; err != nil {
		return nil, err
	}
	if !s.Locked {
		a.selectTake(s, &t, s.ContentHash)
	}
	return &t, nil
}

// AdjustAlignment re-slices a segment from the merged recording with new boundaries.
func (a *App) AdjustAlignment(ctx context.Context, x *Access, r *model.Recording, segmentID string, startMs, endMs int64) (*model.Take, error) {
	if err := need(x, PermAlign); err != nil {
		return nil, err
	}
	if endMs-startMs < 100 {
		return nil, Bad("切片太短")
	}
	src, _, err := a.AssetPath(r.MergedAsset)
	if err != nil {
		return nil, Bad("合并录音不存在，请重新对轨")
	}
	var s model.Segment
	if a.DB.First(&s, "id = ? AND chapter_id = ?", segmentID, r.ChapterID).Error != nil {
		return nil, NotFound("片段")
	}
	t, err := a.sliceTake(ctx, x.Book, &s, src, startMs, endMs, x.User.ID, r.ID)
	if err != nil {
		return nil, err
	}
	res := r.Result.V
	found := false
	for i := range res.Matches {
		if res.Matches[i].SegmentID == segmentID {
			res.Matches[i].StartMs, res.Matches[i].EndMs, res.Matches[i].TakeID = startMs, endMs, t.ID
			found = true
		}
	}
	if !found {
		res.Matches = append(res.Matches, model.AlignMatch{SegmentID: segmentID, StartMs: startMs, EndMs: endMs, Confidence: 1, TakeID: t.ID, Alternatives: []model.AlignSpan{}})
		var missing []string
		for _, m := range res.Missing {
			if m != segmentID {
				missing = append(missing, m)
			}
		}
		res.Missing = missing
		if res.Missing == nil {
			res.Missing = []string{}
		}
	}
	a.DB.Model(r).Update("result", model.JSON[model.AlignResult]{V: res})
	a.emit(x.StudioID, eventSegments(s.BookID, s.ChapterID, a.SegmentViews([]model.Segment{s})))
	return t, nil
}
