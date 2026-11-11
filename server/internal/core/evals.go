package core

import (
	"context"
	"fmt"
	"strings"

	"voicebook/internal/jobs"
	"voicebook/internal/model"
	"voicebook/internal/text"
)

type promptOverrideKey struct{}

// WithPromptOverride makes AI calls in ctx use an unsaved prompt (prompt test bench / evals).
func WithPromptOverride(ctx context.Context, key, content string) context.Context {
	m, _ := ctx.Value(promptOverrideKey{}).(map[string]string)
	n := map[string]string{}
	for k, v := range m {
		n[k] = v
	}
	n[key] = content
	return context.WithValue(ctx, promptOverrideKey{}, n)
}

func (a *App) promptFor(ctx context.Context, key string) string {
	if m, ok := ctx.Value(promptOverrideKey{}).(map[string]string); ok {
		if v, ok := m[key]; ok && strings.TrimSpace(v) != "" {
			return v
		}
	}
	return a.Prompt(key)
}

func evalCharacters(item model.EvalItem) []model.Character {
	chars := []model.Character{{Base: model.Base{ID: "narrator"}, Name: "旁白", Kind: model.CharNarrator}}
	for i, c := range item.Characters {
		ch := model.Character{Base: model.Base{ID: fmt.Sprintf("c%d", i)}, Name: c.Name, Gender: c.Gender, Kind: model.CharSupporting}
		ch.Aliases.V = c.Aliases
		chars = append(chars, ch)
	}
	return chars
}

// RunEval scores script annotation against labeled samples.
func (a *App) RunEval(ctx context.Context, set *model.EvalSet, h *LLMHandle, progress func(float64, string)) (map[string]any, error) {
	var total, speakerOK, typeOK, emotionOK int
	var failures []map[string]any
	for i, item := range set.Items.V {
		progress(float64(i)/float64(len(set.Items.V)), fmt.Sprintf("评测样本 %d/%d", i+1, len(set.Items.V)))
		units := text.Segment(item.Text, 120)
		chars := evalCharacters(item)
		res, err := a.AnnotateUnits(ctx, h, units, chars, false, func(float64, string) {})
		if err != nil {
			return nil, err
		}
		_, codes := describeCharacters(chars)
		nameOf := map[string]string{"NARRATOR": "旁白"}
		for code, id := range codes {
			for _, c := range chars {
				if c.ID == id {
					nameOf[code] = c.Name
				}
			}
		}
		for _, exp := range item.Expected {
			lb, ok := res.Labels[exp.Index]
			total++
			got := nameOf[lb.Speaker]
			if strings.HasPrefix(lb.Speaker, "NEW:") {
				got = strings.TrimPrefix(lb.Speaker, "NEW:")
			}
			if ok && got == exp.Speaker {
				speakerOK++
			} else if len(failures) < 50 {
				u := ""
				if exp.Index < len(units) {
					u = units[exp.Index].Text
				}
				failures = append(failures, map[string]any{"sample": i, "unit": exp.Index, "text": u, "expected": exp.Speaker, "got": got})
			}
			if ok && (exp.Type == "" || lb.Type == exp.Type) {
				typeOK++
			}
			if ok && (exp.Emotion == "" || lb.Emotion == exp.Emotion) {
				emotionOK++
			}
		}
	}
	pct := func(n int) float64 {
		if total == 0 {
			return 0
		}
		return float64(int(float64(n)/float64(total)*1000)) / 10
	}
	return map[string]any{"labels": total, "speakerAccuracy": pct(speakerOK), "typeAccuracy": pct(typeOK), "emotionAccuracy": pct(emotionOK), "failures": failures, "model": h.Client.Model(), "account": h.Account.Name}, nil
}

func (a *App) jobEvalRun(ctx context.Context, jc *jobs.Context) error {
	var set model.EvalSet
	if err := a.DB.First(&set, "id = ?", jc.Job.RefID).Error; err != nil {
		return jobs.Permanent(err)
	}
	var h *LLMHandle
	var err error
	if accID, _ := jc.Job.Payload.V["accountId"].(string); accID != "" {
		var acc model.ProviderAccount
		if err := a.DB.First(&acc, "id = ?", accID).Error; err != nil {
			return jobs.Permanent(err)
		}
		h, err = a.llmFromAccount(&acc, nil, false)
	} else {
		h, err = a.LLMFor(FeatScript, "", "")
	}
	if err != nil {
		return jobs.Permanent(err)
	}
	if content, _ := jc.Job.Payload.V["prompt"].(string); content != "" {
		ctx = WithPromptOverride(ctx, PromptScript, content)
	}
	res, err := a.RunEval(ctx, &set, h, jc.Progress)
	if err != nil {
		return jobs.Permanent(err)
	}
	res["ranAt"] = now()
	res["promptVersion"] = jc.Job.Payload.V["promptVersion"]
	hist := set.History.V
	hist = append([]map[string]any{{"ranAt": res["ranAt"], "speakerAccuracy": res["speakerAccuracy"], "typeAccuracy": res["typeAccuracy"], "emotionAccuracy": res["emotionAccuracy"], "model": res["model"], "promptVersion": res["promptVersion"]}}, hist...)
	if len(hist) > 30 {
		hist = hist[:30]
	}
	a.DB.Model(&set).Updates(map[string]any{"last_run": model.Map{V: res}, "history": model.JSON[[]map[string]any]{V: hist}})
	jc.SetResult(res)
	jc.Progress(1, fmt.Sprintf("说话人准确率 %.1f%%", res["speakerAccuracy"]))
	return nil
}

// TestPrompt runs a prompt against a sample text and returns the raw labels (prompt test bench).
func (a *App) TestPrompt(ctx context.Context, key, content, sample, accountID string) (any, error) {
	var h *LLMHandle
	var err error
	if accountID != "" {
		var acc model.ProviderAccount
		if err := a.DB.First(&acc, "id = ?", accountID).Error; err != nil {
			return nil, NotFound("模型账号")
		}
		h, err = a.llmFromAccount(&acc, nil, false)
	} else {
		h, err = a.LLMFor(FeatScript, "", "")
	}
	if err != nil {
		return nil, err
	}
	ctx = WithPromptOverride(ctx, key, content)
	switch key {
	case PromptScript:
		units := text.Segment(sample, 120)
		chars := []model.Character{{Base: model.Base{ID: "narrator"}, Name: "旁白", Kind: model.CharNarrator}}
		res, err := a.AnnotateUnits(ctx, h, units, chars, false, func(float64, string) {})
		if err != nil {
			return nil, err
		}
		var out []map[string]any
		for _, u := range units {
			lb := res.Labels[u.Index]
			out = append(out, map[string]any{"id": u.Index, "text": u.Text, "type": lb.Type, "speaker": lb.Speaker, "emotion": lb.Emotion, "confidence": lb.Confidence, "prons": lb.Prons})
		}
		return map[string]any{"units": out, "sfx": res.Sfx}, nil
	case PromptCharacters:
		ch := model.Chapter{SourceText: sample, Index: 1}
		infos, _, err := a.AnalyzeCharacters(ctx, h, []model.Chapter{ch}, func(float64, string) {})
		return map[string]any{"characters": infos}, err
	default:
		return nil, Bad("该提示词暂不支持在测试台运行，请在实际功能中验证")
	}
}
