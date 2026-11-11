package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"voicebook/internal/align"
	"voicebook/internal/audio"
	"voicebook/internal/jobs"
	"voicebook/internal/model"
)

func (a *App) EnqueueReview(x *Access, ch *model.Chapter, autoIssue bool) (*model.Job, error) {
	if !x.Can(PermReview) && !x.Can(PermPost) {
		return nil, Forbidden("")
	}
	if err := a.RequireFeature(x.StudioID, model.FeatReview, "智能审听"); err != nil {
		return nil, err
	}
	if ch.CurrentScriptID == "" {
		return nil, Bad("该章节还没有画本")
	}
	run := &model.ReviewRun{ChapterID: ch.ID, BookID: ch.BookID, Status: "running", CreatedBy: x.User.ID, Summary: model.EmptyMap()}
	run.Findings.V = []model.ReviewFinding{}
	a.DB.Create(run)
	j := &model.Job{Type: "review", StudioID: x.StudioID, BookID: ch.BookID, ChapterID: ch.ID, RefType: "review", RefID: run.ID, Title: "智能审听 · " + ch.Title, CreatedBy: x.User.ID, MaxAttempts: 1,
		Payload: model.Map{V: map[string]any{"autoIssue": autoIssue}}}
	if err := a.Q.Enqueue(j); err != nil {
		return nil, err
	}
	a.DB.Model(run).Update("job_id", j.ID)
	return j, nil
}

func (a *App) jobReview(ctx context.Context, jc *jobs.Context) error {
	j := jc.Job
	var run model.ReviewRun
	if err := a.DB.First(&run, "id = ?", j.RefID).Error; err != nil {
		return jobs.Permanent(err)
	}
	var ch model.Chapter
	a.DB.First(&ch, "id = ?", run.ChapterID)
	var b model.Book
	a.DB.First(&b, "id = ?", ch.BookID)
	fail := func(err error) error {
		a.DB.Model(&run).Updates(map[string]any{"status": "failed", "summary": model.Map{V: map[string]any{"error": err.Error()}}})
		return jobs.Permanent(err)
	}
	tl, err := a.BuildTimeline(&ch, false)
	if err != nil {
		return fail(err)
	}
	segs := map[string]model.Segment{}
	for _, s := range a.scriptSegments(ch.CurrentScriptID) {
		segs[s.ID] = s
	}
	rate := b.Settings.V.SpeechRate
	if rate <= 0 {
		rate = 220
	}
	var findings []model.ReviewFinding
	addF := func(f model.ReviewFinding) {
		f.ID = model.NewID()[:12]
		findings = append(findings, f)
	}
	// technical checks per line
	jc.Progress(0.05, "正在检查每句的音量、底噪与语速")
	takeAssets := map[string]string{}
	for i, d := range tl.Dialogue {
		if d.Missing {
			addF(model.ReviewFinding{SegmentID: d.SegmentID, Type: "missing_audio", Severity: "high", Message: "该句没有音频", AtMs: d.StartMs})
			continue
		}
		takeAssets[d.SegmentID] = d.AssetID
		path, _, err := a.AssetPath(d.AssetID)
		if err != nil {
			continue
		}
		pcm, err := a.FF.DecodePCM(ctx, path, 16000, 1, "")
		if err != nil {
			continue
		}
		st := audio.Analyze(pcm)
		if st.RMSDB < -32 {
			addF(model.ReviewFinding{SegmentID: d.SegmentID, Type: "volume", Severity: "medium", Message: fmt.Sprintf("音量偏小（%.0f dB）", st.RMSDB), AtMs: d.StartMs})
		}
		if st.ClippedSamples > 20 {
			addF(model.ReviewFinding{SegmentID: d.SegmentID, Type: "clipping", Severity: "high", Message: fmt.Sprintf("检测到爆音（%d 处削波）", st.ClippedSamples), AtMs: d.StartMs})
		}
		if st.NoiseFloorDB > -42 {
			addF(model.ReviewFinding{SegmentID: d.SegmentID, Type: "noise", Severity: "medium", Message: fmt.Sprintf("底噪偏高（%.0f dB）", st.NoiseFloorDB), AtMs: d.StartMs})
		}
		if st.LongestGapMs > 1800 {
			addF(model.ReviewFinding{SegmentID: d.SegmentID, Type: "silence", Severity: "low", Message: fmt.Sprintf("句中有 %.1f 秒的长静音", float64(st.LongestGapMs)/1000), AtMs: d.StartMs})
		}
		chars := utf8.RuneCountInString(strings.TrimSpace(d.Text))
		if chars >= 6 && st.SpeechMs > 0 {
			expected := float64(chars) * 60000 / float64(rate)
			ratio := float64(d.DurationMs) / expected
			if ratio < 0.5 {
				addF(model.ReviewFinding{SegmentID: d.SegmentID, Type: "speed", Severity: "low", Message: "语速明显偏快", AtMs: d.StartMs})
			} else if ratio > 2.0 {
				addF(model.ReviewFinding{SegmentID: d.SegmentID, Type: "speed", Severity: "low", Message: "语速明显偏慢", AtMs: d.StartMs})
			}
		}
		if i > 0 && d.GapMs > 5000 {
			addF(model.ReviewFinding{SegmentID: d.SegmentID, Type: "pause", Severity: "low", Message: fmt.Sprintf("与上一句间隔 %.1f 秒，可能过长", float64(d.GapMs)/1000), AtMs: d.StartMs})
		}
		jc.Progress(0.05+0.3*float64(i)/float64(len(tl.Dialogue)), "正在检查音频质量")
	}
	// text checks with speech recognition over the dialogue-only mix
	usedASR := false
	if prov, acc, err := a.ASRFor(b.StudioID); err == nil {
		dir, err := a.Store.TempDir("review")
		if err == nil {
			defer os.RemoveAll(dir)
			var clips []audio.MixClip
			for _, d := range tl.Dialogue {
				if !d.Missing {
					if p, _, err := a.AssetPath(d.AssetID); err == nil {
						clips = append(clips, audio.MixClip{Source: p, StartMs: d.StartMs, DurationMs: d.DurationMs, Voice: true})
					}
				}
			}
			mixPath := filepath.Join(dir, "dialogue.wav")
			if _, err := a.FF.Mix(ctx, clips, mixPath, audio.MixOptions{TotalMs: tl.DurationMs}); err == nil {
				jc.Progress(0.4, "正在识别整章语音")
				words, _, err := a.transcribeLong(ctx, prov, mixPath, b.Language, func(p float64) { jc.Progress(0.4+p*0.45, "正在识别整章语音") })
				if err == nil {
					usedASR = true
					a.ChargeCredits(Charge{StudioID: b.StudioID, UserID: j.CreatedBy, BookID: b.ID, ChapterID: ch.ID, JobID: j.ID, Feature: FeatReview, Quantity: float64(tl.DurationMs) / 1000, Unit: "seconds", Provider: acc.Provider, AccountID: acc.ID})
					for _, d := range tl.Dialogue {
						if d.Missing {
							continue
						}
						var heard strings.Builder
						for _, w := range words {
							mid := (w.StartMs + w.EndMs) / 2
							if mid >= d.StartMs-150 && mid <= d.StartMs+d.DurationMs+150 {
								heard.WriteString(w.Text)
							}
						}
						fs, score := align.Compare(d.Text, heard.String())
						if score >= 0.97 {
							continue
						}
						for _, f := range fs {
							sev := "medium"
							n := utf8.RuneCountInString(f.Expected + f.Actual)
							if n >= 4 {
								sev = "high"
							} else if n <= 1 && score > 0.9 {
								sev = "low"
							}
							msg := ""
							switch f.Type {
							case "missing":
								msg = "漏读「" + f.Expected + "」"
							case "extra":
								msg = "多读「" + f.Actual + "」"
							case "wrong":
								msg = "「" + f.Expected + "」疑似读成「" + f.Actual + "」"
							}
							addF(model.ReviewFinding{SegmentID: d.SegmentID, Type: f.Type, Severity: sev, Message: msg, Start: f.Start, End: f.End, Expected: f.Expected, Actual: f.Actual, AtMs: d.StartMs})
						}
					}
				}
			}
		}
	}
	jc.Progress(0.9, "正在整理审听结果")
	counts := map[string]int{}
	bySeg := map[string][]string{}
	for _, f := range findings {
		counts[f.Type]++
		counts[f.Severity]++
		if f.Severity != "low" {
			bySeg[f.SegmentID] = append(bySeg[f.SegmentID], f.Message)
		}
	}
	// reset QC flags from previous runs, then flag current suspects
	a.DB.Model(&model.Segment{}).Where("script_id = ? AND qc = ?", ch.CurrentScriptID, "suspect").Updates(map[string]any{"qc": "", "qc_note": ""})
	for id, msgs := range bySeg {
		a.DB.Model(&model.Segment{}).Where("id = ?", id).Updates(map[string]any{"qc": "suspect", "qc_note": truncateStr(strings.Join(msgs, "；"), 480)})
	}
	summary := map[string]any{"total": len(findings), "counts": counts, "segments": len(tl.Dialogue), "flagged": len(bySeg), "usedAsr": usedASR}
	if !usedASR {
		summary["note"] = "未配置语音识别服务，本次只做了音频技术检测（未比对文字）"
	}
	auto, _ := j.Payload.V["autoIssue"].(bool)
	if auto {
		for i, f := range findings {
			if f.Severity == "low" || f.Type == "missing_audio" {
				continue
			}
			s := segs[f.SegmentID]
			iss := a.createIssueFromFinding(&b, &ch, &s, f, j.CreatedBy)
			if iss != nil {
				findings[i].IssueID = iss.ID
			}
		}
	}
	a.DB.Model(&run).Updates(map[string]any{"status": "done", "used_asr": usedASR, "summary": model.Map{V: summary}, "findings": model.JSON[[]model.ReviewFinding]{V: nonNilFindings(findings)}})
	jc.SetResult(map[string]any{"reviewId": run.ID, "findings": len(findings)})
	jc.Progress(1, fmt.Sprintf("审听完成：发现 %d 个问题，涉及 %d 句", len(findings), len(bySeg)))
	a.emit(b.StudioID, eventChapter(b.ID, ch.ID, "review"))
	return nil
}

func nonNilFindings(f []model.ReviewFinding) []model.ReviewFinding {
	if f == nil {
		return []model.ReviewFinding{}
	}
	return f
}

var issueTypeFromFinding = map[string]string{"missing": "missing", "extra": "extra", "wrong": "wrong", "volume": "noise", "clipping": "noise", "noise": "noise", "silence": "edit", "speed": "performance", "pause": "edit"}

// createIssueFromFinding opens an issue assigned to whoever produced the line's audio.
func (a *App) createIssueFromFinding(b *model.Book, ch *model.Chapter, s *model.Segment, f model.ReviewFinding, reporter string) *model.Issue {
	assignee := ""
	if s.SelectedTakeID != "" {
		var t model.Take
		if a.DB.First(&t, "id = ?", s.SelectedTakeID).Error == nil {
			assignee = t.CreatedBy
		}
	}
	var c model.Character
	if a.DB.First(&c, "id = ?", s.CharacterID).Error == nil && c.Mode == "human" && c.AssigneeID != "" {
		assignee = c.AssigneeID
	}
	at := f.AtMs
	iss := &model.Issue{StudioID: b.StudioID, BookID: b.ID, ChapterID: ch.ID, SegmentID: s.ID, AtMs: &at, Type: firstNonEmpty(issueTypeFromFinding[f.Type], "other"), Source: "auto_review",
		Title: f.Message, Description: "智能审听发现：" + f.Message + "\n台词：" + s.Text, Status: model.IssueOpen, AssigneeID: assignee, ReporterID: reporter, ReviewerID: reporter,
		OldTakeID: s.SelectedTakeID, Severity: f.Severity, Detail: model.Map{V: map[string]any{"findingId": f.ID, "expected": f.Expected, "actual": f.Actual, "start": f.Start, "end": f.End}}}
	if a.DB.Create(iss).Error != nil {
		return nil
	}
	if assignee != "" {
		a.Notify(assignee, b.StudioID, "issue", "你有新的返修工单", f.Message, fmt.Sprintf("/books/%s/chapters/%s?segment=%s", b.ID, ch.ID, s.ID))
	}
	return iss
}

// FindingAction ignores a finding or converts it into an issue.
func (a *App) FindingAction(x *Access, run *model.ReviewRun, findingID, action, assignee string) (*model.Issue, error) {
	if !x.Can(PermReview) && !x.Can(PermPost) {
		return nil, Forbidden("")
	}
	fs := run.Findings.V
	for i := range fs {
		if fs[i].ID != findingID {
			continue
		}
		var iss *model.Issue
		switch action {
		case "ignore":
			fs[i].Ignored = true
		case "unignore":
			fs[i].Ignored = false
		case "issue":
			var ch model.Chapter
			a.DB.First(&ch, "id = ?", run.ChapterID)
			var s model.Segment
			a.DB.First(&s, "id = ?", fs[i].SegmentID)
			iss = a.createIssueFromFinding(x.Book, &ch, &s, fs[i], x.User.ID)
			if iss != nil {
				if assignee != "" {
					a.DB.Model(iss).Update("assignee_id", assignee)
				}
				fs[i].IssueID = iss.ID
			}
		default:
			return nil, Bad("未知操作")
		}
		a.DB.Model(run).Update("findings", model.JSON[[]model.ReviewFinding]{V: fs})
		return iss, nil
	}
	return nil, NotFound("审听结果")
}

// ConcludeReview marks a chapter passed or rejected after (human) review.
func (a *App) ConcludeReview(x *Access, ch *model.Chapter, pass bool, reason, backTo string) error {
	if err := need(x, PermReview); err != nil {
		return err
	}
	if pass {
		var open int64
		a.DB.Model(&model.Issue{}).Where("chapter_id = ? AND status NOT IN ?", ch.ID, []string{model.IssueClosed}).Count(&open)
		if open > 0 {
			return Bad("还有 %d 个未关闭的工单，请处理后再通过", open)
		}
		a.DB.Model(ch).Update("review_status", "passed")
		a.AdvanceTaskOnEvent(ch.ID, "review")
		a.Activity(x.StudioID, ch.BookID, x.User.ID, "review.pass", "chapter", ch.ID, "审听通过「"+ch.Title+"」")
		a.maybeCompleteBook(ch.BookID)
	} else {
		a.DB.Model(ch).Update("review_status", "rejected")
		a.RejectToStage(x, ch, backTo, reason)
		a.Activity(x.StudioID, ch.BookID, x.User.ID, "review.reject", "chapter", ch.ID, "审听打回「"+ch.Title+"」："+reason)
	}
	a.emit(x.StudioID, eventChapter(ch.BookID, ch.ID, "review"))
	return nil
}

func (a *App) maybeCompleteBook(bookID string) {
	var total, passed int64
	a.DB.Model(&model.Chapter{}).Where("book_id = ? AND included = ?", bookID, true).Count(&total)
	a.DB.Model(&model.Chapter{}).Where("book_id = ? AND included = ? AND review_status = ?", bookID, true, "passed").Count(&passed)
	if total > 0 && total == passed {
		a.DB.Model(&model.Book{}).Where("id = ? AND status IN ?", bookID, []string{model.BookPreparing, model.BookProducing}).Update("status", model.BookCompleted)
	}
}
