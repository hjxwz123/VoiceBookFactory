package core

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"voicebook/internal/ai/tts"
	dbx "voicebook/internal/db"
	"voicebook/internal/events"
	"voicebook/internal/model"
)

// Custom voices (clones and designs) live on the platform account that created them: a Qianwen voice made with
// account A does not exist on account B, so it is only ever synthesized (and deleted) with account A. Platforms
// that keep custom voices as recordings on this site (tts.Capabilities.LocalVoices) work with any account.
//
// Some platforms cap how many custom voices one account keeps (Qianwen: 1000, clones and designs together). New
// voices go to the best account of the platform that still has room; administrators are told when an account is
// nearly full, and again when none has room, so they can add another account in time.

var customOrigins = []string{model.VoiceCloned, model.VoiceDesigned}

const (
	voiceQuotaWarn = 0.9            // share of the limit at which administrators are warned
	designReserve  = 3              // candidates a running design may create
	designStale    = 2 * time.Hour  // a design still "running" after this has lost its job and reserves nothing
	quotaAlertGap  = 24 * time.Hour // one alert per account and level a day
)

func isCustomOrigin(o string) bool { return o == model.VoiceCloned || o == model.VoiceDesigned }

// voiceBound reports whether a voice only exists on its own platform account.
func voiceBound(v *model.Voice) bool {
	return isCustomOrigin(v.Origin) && v.ProviderAccountID != "" && !platformCaps(v.Provider).LocalVoices
}

// localVoiceProviders are the platforms whose custom voices are kept on this site (fixed per build).
var localVoiceProviders = sync.OnceValue(func() []string {
	out := []string{}
	for _, p := range tts.Names() {
		if platformCaps(p).LocalVoices {
			out = append(out, p)
		}
	}
	return out
})

// VoiceQuota is how many custom voices an account keeps against its platform's limit.
type VoiceQuota struct {
	Used         int  `json:"used"`
	Limit        int  `json:"limit"`
	Full         bool `json:"full"`
	PlatformFull bool `json:"platformFull,omitempty"` // the platform refused a new voice although fewer are counted here
}

// voiceLimit is an account's custom voice limit: config "voiceLimit" when an administrator set one (e.g. after the
// platform raised it), else the platform's documented limit; 0 = no known limit.
func voiceLimit(acc *model.ProviderAccount) int {
	if acc.Kind != model.KindTTS {
		return 0
	}
	if n, ok := acc.Config.V["voiceLimit"].(float64); ok && n > 0 {
		return int(n)
	}
	return platformCaps(acc.Provider).VoiceLimit
}

// customVoicesOn counts the custom voices an account keeps on its platform: saved voices and clones in training,
// plus design candidates nobody has saved or discarded yet (a running design reserves its candidates).
func (a *App) customVoicesOn(accID string) int {
	var saved, training int64
	a.DB.Model(&model.Voice{}).Where("provider_account_id = ? AND origin IN ? AND provider_voice_id <> '' AND status NOT IN ?", accID, customOrigins,
		[]string{model.VoiceFailed, model.VoiceExpired}).Distinct("provider_voice_id").Count(&saved)
	a.DB.Model(&model.Voice{}).Where("provider_account_id = ? AND origin IN ? AND provider_voice_id = '' AND status = ?", accID, customOrigins, model.VoiceTraining).Count(&training)
	n := int(saved + training)
	var designs []model.VoiceDesign
	a.DB.Select("id", "status", "candidates", "created_at").Where("account_id = ? AND status IN ?", accID, []string{"running", "ready"}).Find(&designs)
	for _, d := range designs {
		if d.Status == "running" {
			if now().Sub(d.CreatedAt) < designStale {
				n += designReserve
			}
			continue
		}
		for _, c := range d.Candidates.V {
			if !c.Saved && !c.Discarded && c.ProviderVoiceID != "" {
				n++
			}
		}
	}
	return n
}

// VoiceQuotaOf reports an account's custom voice usage; nil when its platform has no known limit.
func (a *App) VoiceQuotaOf(acc *model.ProviderAccount) *VoiceQuota {
	limit := voiceLimit(acc)
	if limit <= 0 {
		return nil
	}
	q := &VoiceQuota{Used: a.customVoicesOn(acc.ID), Limit: limit, PlatformFull: acc.VoicesFullAt != nil}
	q.Full = q.Used >= q.Limit || q.PlatformFull
	return q
}

// room is how many more custom voices an account takes; -1 = no known limit.
func (a *App) voiceRoom(acc *model.ProviderAccount) int {
	q := a.VoiceQuotaOf(acc)
	if q == nil {
		return -1
	}
	if q.PlatformFull {
		return 0
	}
	return max(q.Limit-q.Used, 0)
}

var errVoiceQuotaFull = &Error{Status: 503, Code: "voice_quota_full",
	Msg: "平台的自定义音色名额已经用完，暂时不能创建新的音色。已经提醒管理员添加新的语音账号，请稍后再试"}

// voiceAccount picks the account a new custom voice is created on: the first account designAccount would use that
// still has room for need voices. skip lists accounts that refused a voice during this attempt. room is what the
// chosen account can still take (-1 = no known limit).
func (a *App) voiceAccount(provider, studioID string, want func(tts.Capabilities) bool, need int, skip map[string]bool) (p tts.Provider, acc *model.ProviderAccount, room int, err error) {
	var accs []model.ProviderAccount
	q := a.DB.Where("kind = ? AND enabled = ? AND (scope = 'platform' OR studio_id = ?)", model.KindTTS, true, studioID)
	if provider != "" {
		q = q.Where("provider IN ?", providerNames(provider))
	}
	q.Order("CASE WHEN provider = 'placeholder' THEN 1 ELSE 0 END, priority desc").Find(&accs) // real platforms before the demo engine
	var full []*model.ProviderAccount
	for i := range accs {
		c := &accs[i]
		if c.Provider == "placeholder" && len(full) > 0 {
			break // real accounts are only full: the demo engine is no substitute for a real voice
		}
		pp, err := tts.New(a.ttsAccount(c))
		if err != nil || !want(pp.Capabilities()) {
			continue
		}
		r := a.voiceRoom(c)
		if skip[c.ID] || (r >= 0 && r < need) {
			full = append(full, c)
			continue
		}
		return pp, c, r, nil
	}
	if len(full) > 0 {
		return nil, nil, 0, errVoiceQuotaFull
	}
	return nil, nil, 0, &Error{Status: 503, Code: "tts_not_configured", Msg: "没有支持该功能的语音平台账号"}
}

// markVoicesFull records that the platform refused a new custom voice on the account.
func (a *App) markVoicesFull(acc *model.ProviderAccount) {
	t := now()
	acc.VoicesFullAt = &t
	a.DB.Model(&model.ProviderAccount{}).Where("id = ?", acc.ID).Update("voices_full_at", &t)
	a.alertVoiceQuota(acc)
}

// voicesFreed clears the "platform says full" mark once a custom voice was deleted from the account.
func (a *App) voicesFreed(accID string) {
	if accID != "" {
		a.DB.Model(&model.ProviderAccount{}).Where("id = ? AND voices_full_at IS NOT NULL", accID).Update("voices_full_at", nil)
	}
}

// voiceProvider is the adapter for a voice's own account, enabled or not (used to delete it from the platform).
func (a *App) voiceProvider(v *model.Voice) (tts.Provider, *model.ProviderAccount, error) {
	if !voiceBound(v) {
		return a.TTSFor(v)
	}
	var acc model.ProviderAccount
	if err := a.DB.First(&acc, "id = ?", v.ProviderAccountID).Error; err != nil {
		return nil, nil, err
	}
	p, err := tts.New(a.ttsAccount(&acc))
	return p, &acc, err
}

// voiceAccountState caches which TTS accounts are enabled, so voice lists can mark voices whose account is off.
var voiceAccountState struct {
	sync.Mutex
	at      time.Time
	enabled map[string]bool
}

func (a *App) enabledTTSAccounts() map[string]bool {
	voiceAccountState.Lock()
	defer voiceAccountState.Unlock()
	if voiceAccountState.enabled != nil && time.Since(voiceAccountState.at) < 15*time.Second {
		return voiceAccountState.enabled
	}
	var ids []string
	a.DB.Model(&model.ProviderAccount{}).Where("kind = ? AND enabled = ?", model.KindTTS, true).Pluck("id", &ids)
	m := make(map[string]bool, len(ids))
	for _, id := range ids {
		m[id] = true
	}
	voiceAccountState.enabled, voiceAccountState.at = m, time.Now()
	return m
}

func dropEnabledTTSAccounts() {
	voiceAccountState.Lock()
	voiceAccountState.enabled = nil
	voiceAccountState.Unlock()
}

// voiceUsable reports whether a voice can be synthesized now; a custom voice whose own account was disabled or
// removed cannot (no other account has it).
func (a *App) voiceUsable(v *model.Voice) bool {
	return !voiceBound(v) || a.enabledTTSAccounts()[v.ProviderAccountID]
}

// UsableVoices limits a voice query to voices that can be synthesized: account-bound custom voices only while
// their account is enabled.
func (a *App) UsableVoices() (string, []any) {
	ids := make([]string, 0, 8)
	for id := range a.enabledTTSAccounts() {
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		ids = append(ids, "-")
	}
	return "(origin NOT IN ? OR provider_account_id = '' OR provider IN ? OR provider_account_id IN ?)", []any{customOrigins, append(append([]string{}, localVoiceProviders()...), "-"), ids}
}

// alertVoiceQuota tells administrators that an account is nearly full or full, and whether new custom voices still
// have somewhere to go. At most one message per account and level a day.
func (a *App) alertVoiceQuota(acc *model.ProviderAccount) {
	q := a.VoiceQuotaOf(acc)
	if q == nil || (!q.Full && float64(q.Used) < float64(q.Limit)*voiceQuotaWarn) {
		return
	}
	platform := firstNonEmpty(platformNames[acc.Provider], acc.Provider)
	others := 0 // other enabled accounts of the platform with room
	var accs []model.ProviderAccount
	a.DB.Where("kind = ? AND enabled = ? AND scope = ? AND provider IN ? AND id <> ?", model.KindTTS, true, "platform", providerNames(acc.Provider), acc.ID).Find(&accs)
	for i := range accs {
		if r := a.voiceRoom(&accs[i]); r != 0 {
			others++
		}
	}
	level, title := "warn", fmt.Sprintf("%s账号「%s」的自定义音色快满了", platform, acc.Name)
	body := fmt.Sprintf("已用 %d / %d 个。", q.Used, q.Limit)
	if q.Full {
		level, title = "full", fmt.Sprintf("%s账号「%s」的自定义音色已满", platform, acc.Name)
		if q.PlatformFull && q.Used < q.Limit {
			body = fmt.Sprintf("%s拒绝了新的音色（本站记录 %d / %d 个，账号里可能还有在别处创建的音色）。", platform, q.Used, q.Limit)
		}
	}
	switch {
	case others > 0:
		body += fmt.Sprintf("新的克隆和设计音色会自动使用其他%s账号；建议提前再添加一个账号备用。", platform)
	case q.Full:
		title = platform + "的自定义音色名额已用完"
		body += "用户现在无法克隆或设计新的音色，请尽快在「AI 供应商 → 语音合成」添加一个新的" + platform + "账号。"
	default:
		body += "满了以后用户将无法克隆或设计新的音色，请提前在「AI 供应商 → 语音合成」添加一个新的" + platform + "账号。"
	}
	if acc.Provider == "qwen" || acc.Provider == "aliyun" {
		body += "注意：名额按千问 AI 平台账号计算，同一个账号下的多个 API Key 共用这 1000 个名额，新账号要用另一个千问 AI 平台账号的 API Key。"
	}
	key := "voice_quota:" + acc.ID + ":" + level
	if q.Full && others == 0 { // the platform as a whole is out of room: one message, whichever account noticed
		key = "voice_quota:" + platform + ":exhausted"
	}
	a.notifyOps(key, title, body, "/admin/providers")
}

// notifyOps sends an in-app message to platform administrators (super admins and operators), once per key a day.
func (a *App) notifyOps(key, title, body, link string) {
	since := now().Add(-quotaAlertGap)
	var n int64
	a.DB.Model(&model.Notification{}).Where("type = ? AND created_at > ? AND "+dbx.Like(a.DB, "data"), "ops_alert", since, "%\""+key+"\"%").Count(&n)
	if n > 0 {
		return
	}
	var admins []model.User
	a.DB.Where("status = ? AND ("+dbx.Like(a.DB, "admin_roles")+" OR "+dbx.Like(a.DB, "admin_roles")+")", "active",
		"%\""+model.AdminSuper+"\"%", "%\""+model.AdminOps+"\"%").Find(&admins)
	for _, u := range admins {
		nt := model.Notification{UserID: u.ID, Type: "ops_alert", Title: title, Body: truncateStr(body, 900), Link: link, Data: model.Map{V: map[string]any{"key": key}}}
		if err := a.DB.Create(&nt).Error; err != nil {
			slog.Warn("ops alert", "key", key, "err", err)
			continue
		}
		a.emitUser(u.ID, events.Event{Type: "notification", Data: nt})
	}
}

// discardVoice deletes a custom voice the platform created but this site will not keep (failed review, charge
// failed, job stopped), so it does not take one of the account's slots.
func discardVoice(ctx context.Context, p tts.Provider, id string) {
	if id == "" {
		return
	}
	if cl, ok := p.(tts.VoiceCloner); ok {
		if err := cl.DeleteVoice(context.WithoutCancel(ctx), id); err != nil && !errors.Is(err, tts.ErrNotImplemented) {
			slog.Warn("discard platform voice", "voice", id, "err", err)
		}
	}
}

// BoundVoices counts the ready custom voices that only work on this account (they stop working when it is
// disabled or deleted).
func (a *App) BoundVoices(acc *model.ProviderAccount) int64 {
	if acc.Kind != model.KindTTS || platformCaps(acc.Provider).LocalVoices {
		return 0
	}
	var n int64
	a.DB.Model(&model.Voice{}).Where("provider_account_id = ? AND origin IN ? AND status = ?", acc.ID, customOrigins, model.VoiceReady).Count(&n)
	return n
}

// VoiceQuotaAlerts lists accounts that are nearly full or full, for the admin dashboard.
func (a *App) VoiceQuotaAlerts() []map[string]any {
	var accs []model.ProviderAccount
	a.DB.Where("kind = ? AND enabled = ? AND scope = ?", model.KindTTS, true, "platform").Order("priority desc").Find(&accs)
	type agg struct{ accounts, full int }
	per := map[string]*agg{}
	var out []map[string]any
	for i := range accs {
		acc := &accs[i]
		q := a.VoiceQuotaOf(acc)
		if q == nil {
			continue
		}
		platform := firstNonEmpty(platformNames[acc.Provider], acc.Provider)
		key := platform // qwen and the older aliyun accounts are one platform
		if per[key] == nil {
			per[key] = &agg{}
		}
		per[key].accounts++
		switch {
		case q.Full:
			per[key].full++
			out = append(out, map[string]any{"level": "warning", "title": platform + "账号「" + acc.Name + "」的自定义音色已满",
				"detail": fmt.Sprintf("已用 %d / %d 个，新的克隆和设计音色会使用其他%s账号", q.Used, q.Limit, platform), "link": "/admin/providers"})
		case float64(q.Used) >= float64(q.Limit)*voiceQuotaWarn:
			out = append(out, map[string]any{"level": "warning", "title": platform + "账号「" + acc.Name + "」的自定义音色快满了",
				"detail": fmt.Sprintf("已用 %d / %d 个，建议提前添加一个新的%s账号", q.Used, q.Limit, platform), "link": "/admin/providers"})
		}
	}
	for platform, g := range per {
		if g.accounts > 0 && g.full == g.accounts {
			out = append([]map[string]any{{"level": "danger", "title": platform + "的自定义音色名额已用完",
				"detail": "用户无法克隆或设计新的音色，请添加一个新的" + platform + "账号", "link": "/admin/providers"}}, out...)
		}
	}
	return out
}
