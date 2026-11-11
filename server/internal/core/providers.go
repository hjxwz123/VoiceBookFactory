package core

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"voicebook/internal/ai/asr"
	"voicebook/internal/ai/gen"
	"voicebook/internal/ai/llm"
	"voicebook/internal/ai/tts"
	"voicebook/internal/model"
)

func (a *App) accountKey(acc *model.ProviderAccount) string {
	return a.Decrypt(acc.CredentialsEnc)
}

// pickAccount returns the account for a kind: studio BYOK first (when preferred), then routing, then priority.
func (a *App) pickAccount(kind, feature, tier, studioID string) (*model.ProviderAccount, *model.RoutingRule, bool, error) {
	if studioID != "" {
		var st model.Studio
		if a.DB.First(&st, "id = ?", studioID).Error == nil && st.Settings.V.PreferBYOK {
			var acc model.ProviderAccount
			if a.DB.Where("kind = ? AND scope = ? AND studio_id = ? AND enabled = ?", kind, "studio", studioID, true).Order("priority desc").First(&acc).Error == nil {
				return &acc, nil, true, nil
			}
		}
	}
	if feature != "" {
		var rules []model.RoutingRule
		a.DB.Where("feature = ? AND enabled = ? AND (studio_id = ? OR studio_id = '')", feature, true, studioID).
			Order("CASE WHEN studio_id = '' THEN 1 ELSE 0 END").Find(&rules)
		for _, tierWanted := range []string{tier, "*"} {
			for i := range rules {
				r := rules[i]
				if r.Tier != tierWanted && !(tierWanted == "*" && r.Tier == "") {
					continue
				}
				for _, id := range append([]string{r.PrimaryID}, r.Fallbacks.V...) {
					var acc model.ProviderAccount
					if id != "" && a.DB.First(&acc, "id = ? AND enabled = ?", id, true).Error == nil && acc.HealthStatus != "down" {
						return &acc, &r, acc.Scope == "studio", nil
					}
				}
			}
		}
	}
	var acc model.ProviderAccount
	// on equal priority a real account beats the built-in placeholder, so adding one is enough to switch over
	if err := a.DB.Where("kind = ? AND scope = ? AND enabled = ?", kind, "platform", true).Order("priority desc, CASE WHEN provider = 'placeholder' THEN 1 ELSE 0 END, created_at asc").First(&acc).Error; err != nil {
		return nil, nil, false, err
	}
	return &acc, nil, false, nil
}

type LLMHandle struct {
	Client  llm.Client
	Account *model.ProviderAccount
	BYOK    bool
}

var errNoLLM = &Error{Status: 503, Code: "llm_not_configured", Msg: "平台尚未配置可用的大语言模型。请管理员在「管理后台 → AI 供应商」中添加 OpenAI / Claude 协议的模型账号。"}

func (a *App) LLMFor(feature, tier, studioID string) (*LLMHandle, error) {
	acc, rule, byok, err := a.pickAccount(model.KindLLM, feature, tier, studioID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errNoLLM
		}
		return nil, err
	}
	return a.llmFromAccount(acc, rule, byok)
}

func (a *App) llmFromAccount(acc *model.ProviderAccount, rule *model.RoutingRule, byok bool) (*LLMHandle, error) {
	modelName := acc.Model
	if rule != nil && rule.Model != "" {
		modelName = rule.Model
	}
	if modelName == "" && acc.Provider == model.ProtoAnthropic {
		modelName = llm.DefaultClaudeModel
	}
	cfg := llm.FromMap(acc.Provider, acc.BaseURL, a.accountKey(acc), modelName, acc.Config.V)
	c, err := llm.New(cfg)
	if err != nil {
		return nil, Bad("模型账号「%s」配置有误：%v", acc.Name, err)
	}
	return &LLMHandle{Client: c, Account: acc, BYOK: byok}, nil
}

// CallLLM runs a JSON-producing request and records health metrics on the account.
func (a *App) CallLLM(ctx context.Context, h *LLMHandle, req llm.Request, out any) (*llm.Response, error) {
	start := time.Now()
	resp, err := llm.ChatJSON(ctx, h.Client, req, out)
	a.noteCall(h.Account.ID, time.Since(start), err)
	if err != nil {
		return nil, fmt.Errorf("大模型调用失败（%s）：%w", h.Account.Name, err)
	}
	return resp, nil
}

// noteCall records one provider call on the account: call/failure counters, latency and health.
// A cancelled call (user stopped the job) says nothing about the provider and is not counted.
func (a *App) noteCall(accountID string, lat time.Duration, err error) {
	if errors.Is(err, context.Canceled) {
		return
	}
	up := map[string]any{"calls": gorm.Expr("calls + 1"), "total_latency_ms": gorm.Expr("total_latency_ms + ?", lat.Milliseconds())}
	if err != nil {
		up["failures"] = gorm.Expr("failures + 1")
		up["health_status"] = "error"
		up["health_note"] = truncateStr(err.Error(), 480)
	} else {
		up["health_status"] = "ok"
		up["health_note"] = ""
	}
	t := now()
	up["checked_at"] = &t
	a.DB.Model(&model.ProviderAccount{}).Where("id = ?", accountID).Updates(up)
}

func truncateStr(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}

// ttsAccount converts a stored account into adapter settings.
func (a *App) ttsAccount(acc *model.ProviderAccount) tts.Account {
	return tts.Account{Provider: acc.Provider, BaseURL: acc.BaseURL, APIKey: a.accountKey(acc), Model: acc.Model, Config: acc.Config.V, RPM: acc.RPM}
}

// TTSFor returns the synthesis provider for a voice.
func (a *App) TTSFor(v *model.Voice) (tts.Provider, *model.ProviderAccount, error) {
	var acc model.ProviderAccount
	var err error
	if v.ProviderAccountID != "" {
		err = a.DB.First(&acc, "id = ? AND enabled = ?", v.ProviderAccountID, true).Error
	}
	if v.ProviderAccountID == "" || err != nil {
		// unpinned voice, or its account was removed/disabled: any enabled account of the same platform
		err = a.DB.Where("kind = ? AND provider IN ? AND enabled = ? AND (scope = 'platform' OR studio_id = ?)", model.KindTTS, providerNames(v.Provider), true, v.StudioID).
			Order("priority desc").First(&acc).Error
	}
	if err != nil {
		return nil, nil, &Error{Status: 503, Code: "tts_not_configured", Msg: fmt.Sprintf("音色「%s」所属平台（%s）没有可用的账号，请联系管理员启用", v.Name, v.Provider)}
	}
	p, err := tts.New(a.ttsAccount(&acc))
	if err != nil {
		return nil, nil, err
	}
	return p, &acc, nil
}

// TTSAccount returns any enabled TTS account for a provider (used by voice design/clone).
func (a *App) TTSAccount(provider, studioID string) (tts.Provider, *model.ProviderAccount, error) {
	var acc model.ProviderAccount
	q := a.DB.Where("kind = ? AND enabled = ? AND (scope = 'platform' OR studio_id = ?)", model.KindTTS, true, studioID)
	if provider != "" {
		q = q.Where("provider = ?", provider)
	}
	if err := q.Order("priority desc").First(&acc).Error; err != nil {
		return nil, nil, &Error{Status: 503, Code: "tts_not_configured", Msg: "没有可用的语音平台账号"}
	}
	p, err := tts.New(a.ttsAccount(&acc))
	return p, &acc, err
}

func (a *App) ASRFor(studioID string) (asr.Provider, *model.ProviderAccount, error) {
	acc, _, _, err := a.pickAccount(model.KindASR, "asr", "", studioID)
	if err != nil {
		return nil, nil, &Error{Status: 503, Code: "asr_not_configured", Msg: "平台尚未配置语音识别服务（ASR），智能对轨与文本审听不可用。请管理员在「AI 供应商」中添加。"}
	}
	p, err := asr.New(asr.Account{Provider: acc.Provider, BaseURL: acc.BaseURL, APIKey: a.accountKey(acc), Model: acc.Model, Config: acc.Config.V})
	return p, acc, err
}

func (a *App) genAccount(kind, studioID string) (*model.ProviderAccount, bool, error) {
	acc, rule, byok, err := a.pickAccount(kind, kind, "", studioID)
	if err != nil {
		names := map[string]string{model.KindImage: "图片生成", model.KindMusic: "音乐生成", model.KindSFX: "音效生成"}
		return nil, false, &Error{Status: 503, Code: kind + "_not_configured", Msg: "平台尚未配置" + names[kind] + "服务"}
	}
	if rule != nil && rule.Model != "" { // a routing rule may pin a different model on the same account
		cp := *acc
		cp.Model = rule.Model
		acc = &cp
	}
	return acc, byok, nil
}

func (a *App) genAcc(acc *model.ProviderAccount) gen.Account {
	return gen.Account{Provider: acc.Provider, BaseURL: acc.BaseURL, APIKey: a.accountKey(acc), Model: acc.Model, Config: acc.Config.V}
}

// TestAccount performs a lightweight live check of an account.
func (a *App) TestAccount(ctx context.Context, acc *model.ProviderAccount) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	var msg string
	var err error
	switch acc.Kind {
	case model.KindLLM:
		var h *LLMHandle
		h, err = a.llmFromAccount(acc, nil, false)
		if err == nil {
			var out struct {
				Reply string `json:"reply"`
			}
			schema := map[string]any{"type": "object", "properties": map[string]any{"reply": map[string]any{"type": "string"}}, "required": []string{"reply"}, "additionalProperties": false}
			var resp *llm.Response
			resp, err = a.CallLLM(ctx, h, llm.Request{System: "你是连通性测试助手。", Messages: []llm.Message{{Role: "user", Content: "请用 JSON 回复 {\"reply\":\"ok\"}"}}, Schema: schema, MaxTokens: 2000, Effort: "low"}, &out)
			if err == nil {
				msg = fmt.Sprintf("连接成功：模型 %s 返回「%s」，耗时 %dms", resp.Model, out.Reply, resp.LatencyMs)
			}
		}
	case model.KindTTS:
		var p tts.Provider
		p, err = tts.New(a.ttsAccount(acc))
		if err == nil {
			_, err = p.Synthesize(ctx, tts.SynthesizeRequest{Text: "连接测试", Speed: 1})
			if err == nil {
				msg = "语音合成连接正常"
			}
		}
	case model.KindASR:
		msg = "语音识别账号已保存；将在首次对轨或审听时验证"
	default:
		switch acc.Kind {
		case model.KindImage:
			var p gen.ImageProvider
			if p, err = gen.NewImage(a.genAcc(acc)); err == nil {
				if c, ok := p.(gen.Checker); ok { // verify key + model for free instead of paying for a picture
					msg, err = c.Check(ctx)
				} else {
					_, err = p.GenerateImage(ctx, gen.ImageRequest{Prompt: "test", Width: 64, Height: 64})
				}
			}
		case model.KindMusic:
			var p gen.MusicProvider
			if p, err = gen.NewMusic(a.genAcc(acc)); err == nil {
				if c, ok := p.(gen.Checker); ok { // a real song costs money and minutes; check the address and key instead
					msg, err = c.Check(ctx)
				} else {
					_, err = p.GenerateMusic(ctx, gen.MusicRequest{Prompt: "test", DurationMs: 2000})
				}
			}
		case model.KindSFX:
			var p gen.SFXProvider
			if p, err = gen.NewSFX(a.genAcc(acc)); err == nil {
				_, err = p.GenerateSFX(ctx, gen.SFXRequest{Prompt: "test", DurationMs: 1000})
			}
		}
		if err == nil && msg == "" {
			msg = "生成服务连接正常"
		}
	}
	status := "ok"
	note := ""
	if err != nil {
		status, note = "error", truncateStr(err.Error(), 480)
	}
	t := now()
	a.DB.Model(acc).Updates(map[string]any{"health_status": status, "health_note": note, "checked_at": &t})
	return msg, err
}
