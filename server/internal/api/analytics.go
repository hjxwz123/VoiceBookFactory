package api

import (
	"net/http"
	"sort"
	"time"

	"gorm.io/gorm"

	"voicebook/internal/core"
	dbx "voicebook/internal/db"
	"voicebook/internal/model"
)

// The detailed dashboard: growth, revenue, AI cost, production and reliability over a period (7, 30 or
// 90 days), each total with the previous period of the same length for comparison. Daily series are
// zero-filled so the charts line up.

type pair struct {
	Cur  float64 `json:"cur"`
	Prev float64 `json:"prev"`
}

type part struct {
	Key   string  `json:"key"`
	Label string  `json:"label"`
	Value float64 `json:"value"`
	Extra float64 `json:"extra,omitempty"`
}

var featureLabels = map[string]string{
	core.FeatScript: "AI 画本", core.FeatCharacter: "角色分析", core.FeatLexicon: "多音字", core.FeatCasting: "AI 选角", core.FeatTTS: "AI 配音",
	core.FeatPreview: "试听", core.FeatDesign: "音色设计", core.FeatClone: "声音克隆", core.FeatSFX: "音效生成", core.FeatMusic: "音乐生成",
	core.FeatImage: "封面生成", core.FeatAlign: "录音对轨", core.FeatReview: "AI 审听", core.FeatModerate: "内容审核", core.FeatMarket: "音色市场", core.FeatQuick: "快速配音",
}

var ledgerLabels = map[string]string{"grant": "人工发放", "plan": "会员月度积分", "refund": "退回", "promo": "兑换码", "market_income": "音色市场收入", "referral": "邀请奖励", "signup": "注册赠送"}

var jobLabels = map[string]string{"pipeline": "一键成书", "script_parse": "生成画本", "script_sync": "同步画本", "tts_segments": "配音", "render": "章节合成", "export": "导出",
	"character_analysis": "角色分析", "casting": "AI 选角", "voice_clone": "声音克隆", "voice_design": "音色设计", "voice_preview_import": "导入试听", "sfx_generate": "音效生成",
	"sfx_import": "导入音效", "music_generate": "音乐生成", "music_import": "导入音乐", "image_generate": "封面生成", "align": "录音对轨", "review": "AI 审听", "quick_tts": "快速配音",
	"eval_run": "效果评测", "webhook": "Webhook 通知"}

func labelOf(m map[string]string, k string) string {
	if v, ok := m[k]; ok {
		return v
	}
	if k == "" {
		return "其他"
	}
	return k
}

func (s *Server) analytics(r *http.Request) (any, error) {
	days := qint(r, "days", 30)
	if days != 7 && days != 90 {
		days = 30
	}
	db := s.app.DB
	now := time.Now().UTC()
	from := now.AddDate(0, 0, -days)
	prevFrom := from.AddDate(0, 0, -days)
	dayExpr := dbx.Day(db, "created_at")

	// the days of the period, oldest first
	var dayList []string
	for t := from.AddDate(0, 0, 1); !t.After(now); t = t.AddDate(0, 0, 1) {
		dayList = append(dayList, t.Format("2006-01-02"))
	}
	index := map[string]int{}
	for i, d := range dayList {
		index[d] = i
	}
	// series runs "SELECT day, value" over q (already filtered to the period) and lines it up with dayList
	series := func(q *gorm.DB, value string) []float64 {
		out := make([]float64, len(dayList))
		var rows []struct {
			Day string
			V   float64
		}
		q.Select(dayExpr + " AS day, " + value + " AS v").Group("day").Scan(&rows)
		for _, x := range rows {
			if i, ok := index[x.Day]; ok {
				out[i] = x.V
			}
		}
		return out
	}
	m := func(v any) *gorm.DB { return db.Model(v) }
	cnt := func(q *gorm.DB) float64 {
		var n int64
		q.Count(&n)
		return float64(n)
	}
	sum := func(q *gorm.DB, expr string) float64 {
		var v float64
		q.Select("COALESCE(" + expr + ",0)").Scan(&v)
		return v
	}
	period := func(v any, where string, args ...any) pair {
		cur := m(v).Where("created_at > ?", from)
		prev := m(v).Where("created_at > ? AND created_at <= ?", prevFrom, from)
		if where != "" {
			cur, prev = cur.Where(where, args...), prev.Where(where, args...)
		}
		return pair{cnt(cur), cnt(prev)}
	}
	periodSum := func(v any, expr, where string, args ...any) pair {
		cur := m(v).Where("created_at > ?", from)
		prev := m(v).Where("created_at > ? AND created_at <= ?", prevFrom, from)
		if where != "" {
			cur, prev = cur.Where(where, args...), prev.Where(where, args...)
		}
		return pair{sum(cur, expr), sum(prev, expr)}
	}
	groups := func(q *gorm.DB, key, value string, labels map[string]string) []part {
		var rows []struct {
			K string
			V float64
			E float64
		}
		q.Select(key + " AS k, " + value + " AS v, 0 AS e").Group(key).Scan(&rows)
		out := []part{}
		for _, x := range rows {
			out = append(out, part{Key: x.K, Label: labelOf(labels, x.K), Value: x.V})
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Value > out[j].Value })
		return out
	}

	// ---------- growth ----------
	newUsers := func() *gorm.DB { return m(&model.User{}).Select("id").Where("created_at > ?", from) }
	newN := cnt(m(&model.User{}).Where("created_at > ?", from))
	phone := cnt(m(&model.User{}).Where("created_at > ? AND (email IS NULL OR email = '')", from))
	oauth := cnt(m(&model.OAuthIdentity{}).Where("user_id IN (?)", newUsers()).Distinct("user_id"))
	viaReferral := cnt(m(&model.Referral{}).Where("invitee_id IN (?)", newUsers()))
	viaRegCode := cnt(m(&model.RegistrationCodeUse{}).Where("user_id IN (?)", newUsers()))
	var active int64
	m(&model.Session{}).Where("last_used_at > ?", from).Distinct("user_id").Count(&active)
	creators := cnt(m(&model.UsageRecord{}).Where("created_at > ? AND user_id <> ''", from).Distinct("user_id"))
	funnel := []part{
		{Key: "signup", Label: "注册", Value: newN},
		{Key: "book", Label: "导入了书稿", Value: cnt(m(&model.Book{}).Where("created_by IN (?)", newUsers()).Distinct("created_by"))},
		{Key: "voice", Label: "生成过配音", Value: cnt(m(&model.UsageRecord{}).Where("feature IN ? AND user_id IN (?)", []string{core.FeatTTS, core.FeatQuick}, newUsers()).Distinct("user_id"))},
		{Key: "render", Label: "合成过成品", Value: cnt(m(&model.Render{}).Where("status = ? AND created_by IN (?)", "ready", newUsers()).Distinct("created_by"))},
		{Key: "paid", Label: "付费", Value: cnt(m(&model.Payment{}).Where("user_id IN (?)", newUsers()).Distinct("user_id"))},
	}
	growth := map[string]any{
		"newUsers": period(&model.User{}, ""), "activeUsers": active, "creators": creators,
		"series": map[string]any{
			"newUsers": series(m(&model.User{}).Where("created_at > ?", from), "COUNT(*)"),
			"creators": series(m(&model.UsageRecord{}).Where("created_at > ? AND user_id <> ''", from), "COUNT(DISTINCT user_id)"),
		},
		"methods":  []part{{Key: "email", Label: "邮箱", Value: max(newN-phone-oauth, 0)}, {Key: "phone", Label: "手机号", Value: phone}, {Key: "oauth", Label: "第三方账号", Value: oauth}},
		"channels": []part{{Key: "referral", Label: "好友邀请", Value: viaReferral}, {Key: "regcode", Label: "注册码", Value: viaRegCode}, {Key: "direct", Label: "直接注册", Value: max(newN-viaReferral-viaRegCode, 0)}},
		"funnel":   funnel,
	}

	// ---------- revenue ----------
	var memberRows []struct {
		PlanID string
		N      float64
	}
	m(&model.UserMembership{}).Select("plan_id, COUNT(*) AS n").Group("plan_id").Scan(&memberRows)
	planNames := map[string]string{}
	var plans []model.Plan
	db.Select("id", "name", "code").Find(&plans)
	for _, p := range plans {
		planNames[p.ID] = p.Name
	}
	members := []part{}
	for _, x := range memberRows {
		members = append(members, part{Key: x.PlanID, Label: labelOf(planNames, x.PlanID), Value: x.N})
	}
	sort.Slice(members, func(i, j int) bool { return members[i].Value > members[j].Value })
	newPeriods := groups(m(&model.MembershipPeriod{}).Where("created_at > ? AND source <> ?", from, model.MemberSourceTrial), "plan_id", "COUNT(*)", planNames)
	payAmount := periodSum(&model.Payment{}, "SUM(amount)", "")
	payers := pair{cnt(m(&model.Payment{}).Where("created_at > ?", from).Distinct("user_id")), cnt(m(&model.Payment{}).Where("created_at > ? AND created_at <= ?", prevFrom, from).Distinct("user_id"))}
	var outstanding float64
	m(&model.CreditLot{}).Where("remaining > 0 AND (expires_at IS NULL OR expires_at > ?)", now).Select("COALESCE(SUM(remaining),0)").Scan(&outstanding)
	revenue := map[string]any{
		"amount": payAmount, "payments": period(&model.Payment{}, ""), "payers": payers,
		"bySource":    groups(m(&model.Payment{}).Where("created_at > ?", from), "source", "SUM(amount)", map[string]string{"admin": "后台录入", "redeem": "兑换码", "order": "在线支付"}),
		"members":     members,
		"newMembers":  newPeriods,
		"trials":      period(&model.MembershipPeriod{}, "source = ?", model.MemberSourceTrial),
		"expiring7d":  cnt(m(&model.UserMembership{}).Where("expires_at > ? AND expires_at <= ?", now, now.AddDate(0, 0, 7))),
		"granted":     groups(m(&model.CreditLedger{}).Where("created_at > ? AND delta > 0", from), "kind", "SUM(delta)", ledgerLabels),
		"consumed":    periodSum(&model.UsageRecord{}, "SUM(credits)", ""),
		"grantedSum":  periodSum(&model.CreditLedger{}, "SUM(delta)", "delta > 0"),
		"outstanding": outstanding,
		"redeemed":    period(&model.RedeemUse{}, ""),
		"series": map[string]any{
			"amount":   series(m(&model.Payment{}).Where("created_at > ?", from), "SUM(amount)"),
			"consumed": series(m(&model.UsageRecord{}).Where("created_at > ?", from), "SUM(credits)"),
			"granted":  series(m(&model.CreditLedger{}).Where("created_at > ? AND delta > 0", from), "SUM(delta)"),
		},
	}

	// ---------- AI cost ----------
	type featRow struct {
		Feature  string
		Credits  float64
		Cost     float64
		Quantity float64
		Calls    float64
	}
	var feats []featRow
	m(&model.UsageRecord{}).Where("created_at > ?", from).Select("feature, COALESCE(SUM(credits),0) AS credits, COALESCE(SUM(cost_cny),0) AS cost, COALESCE(SUM(quantity),0) AS quantity, COUNT(*) AS calls").Group("feature").Scan(&feats)
	byFeature := []map[string]any{}
	for _, f := range feats {
		byFeature = append(byFeature, map[string]any{"key": f.Feature, "label": labelOf(featureLabels, f.Feature), "credits": f.Credits, "cost": f.Cost, "quantity": f.Quantity, "calls": f.Calls})
	}
	sort.Slice(byFeature, func(i, j int) bool { return byFeature[i]["cost"].(float64) > byFeature[j]["cost"].(float64) })
	var accRows []struct {
		AccountID string
		Provider  string
		Cost      float64
		Calls     float64
	}
	m(&model.UsageRecord{}).Where("created_at > ? AND account_id <> ''", from).Select("account_id, MAX(provider) AS provider, COALESCE(SUM(cost_cny),0) AS cost, COUNT(*) AS calls").Group("account_id").Order("cost desc").Limit(12).Scan(&accRows)
	accNames := map[string]string{}
	var accs []model.ProviderAccount
	db.Select("id", "name").Find(&accs)
	for _, a := range accs {
		accNames[a.ID] = a.Name
	}
	byAccount := []part{}
	for _, x := range accRows {
		byAccount = append(byAccount, part{Key: x.AccountID, Label: firstNonEmptyStr(accNames[x.AccountID], x.Provider), Value: x.Cost, Extra: x.Calls})
	}
	var top []struct {
		StudioID string
		Credits  float64
		Cost     float64
	}
	m(&model.UsageRecord{}).Where("created_at > ?", from).Select("studio_id, COALESCE(SUM(credits),0) AS credits, COALESCE(SUM(cost_cny),0) AS cost").Group("studio_id").Order("credits desc").Limit(10).Scan(&top)
	ids := make([]string, 0, len(top))
	for _, t := range top {
		ids = append(ids, t.StudioID)
	}
	stNames := map[string]string{}
	var sts []model.Studio
	db.Select("id", "name").Where("id IN ?", ids).Find(&sts)
	for _, st := range sts {
		stNames[st.ID] = st.Name
	}
	topStudios := []map[string]any{}
	for _, t := range top {
		topStudios = append(topStudios, map[string]any{"id": t.StudioID, "name": firstNonEmptyStr(stNames[t.StudioID], "已删除的工作室"), "credits": t.Credits, "cost": t.Cost})
	}
	ai := map[string]any{
		"cost": periodSum(&model.UsageRecord{}, "SUM(cost_cny)", ""), "calls": period(&model.UsageRecord{}, ""),
		"ttsChars":  periodSum(&model.UsageRecord{}, "SUM(quantity)", "feature IN ?", []string{core.FeatTTS, core.FeatQuick}),
		"byok":      sum(m(&model.UsageRecord{}).Where("created_at > ? AND byok = ?", from, true), "SUM(credits)"),
		"byFeature": byFeature, "byAccount": byAccount, "topStudios": topStudios,
		"byTier": groups(m(&model.UsageRecord{}).Where("created_at > ? AND feature = ?", from, core.FeatTTS), "tier", "SUM(quantity)", map[string]string{"economy": "经济", "standard": "标准", "premium": "精品"}),
		"series": map[string]any{"cost": series(m(&model.UsageRecord{}).Where("created_at > ?", from), "SUM(cost_cny)")},
	}

	// ---------- production ----------
	pipeStatus := groups(m(&model.PipelineRun{}).Where("created_at > ?", from), "status", "COUNT(*)", map[string]string{"queued": "排队中", "running": "进行中", "paused": "已暂停", "waiting": "等待中", "completed": "已完成", "failed": "未完成", "canceled": "已取消"})
	var runs []model.PipelineRun
	m(&model.PipelineRun{}).Select("started_at", "finished_at").Where("created_at > ? AND status = ? AND started_at IS NOT NULL AND finished_at IS NOT NULL", from, "completed").Limit(2000).Find(&runs)
	var pipeMs []int64
	for _, x := range runs {
		pipeMs = append(pipeMs, x.FinishedAt.Sub(*x.StartedAt).Milliseconds())
	}
	production := map[string]any{
		"books":     period(&model.Book{}, ""),
		"renders":   period(&model.Render{}, "status = ?", "ready"),
		"audioMs":   periodSum(&model.Render{}, "SUM(duration_ms)", "status = ?", "ready"),
		"exports":   period(&model.Export{}, "status = ?", "ready"),
		"clones":    period(&model.Voice{}, "origin = ?", model.VoiceCloned),
		"designs":   period(&model.Voice{}, "origin = ?", model.VoiceDesigned),
		"modes":     groups(m(&model.Book{}).Where("created_at > ?", from), "mode", "COUNT(*)", map[string]string{"single": "单播", "multi": "多播有声书", "drama": "广播剧"}),
		"pipelines": pipeStatus, "pipelineMedianMs": median(pipeMs),
		"series": map[string]any{
			"renders": series(m(&model.Render{}).Where("created_at > ? AND status = ?", from, "ready"), "COUNT(*)"),
			"audioMs": series(m(&model.Render{}).Where("created_at > ? AND status = ?", from, "ready"), "SUM(duration_ms)"),
			"books":   series(m(&model.Book{}).Where("created_at > ?", from), "COUNT(*)"),
		},
	}

	// ---------- reliability ----------
	var jobRows []struct {
		Type   string
		Status string
		N      float64
	}
	m(&model.Job{}).Where("created_at > ? AND parent_id = ''", from).Select("type, status, COUNT(*) AS n").Group("type, status").Scan(&jobRows)
	jt := map[string]map[string]float64{}
	for _, x := range jobRows {
		if jt[x.Type] == nil {
			jt[x.Type] = map[string]float64{}
		}
		jt[x.Type][x.Status] += x.N
	}
	// durations of recent finished jobs, per type
	var done []model.Job
	m(&model.Job{}).Select("type", "started_at", "finished_at").Where("created_at > ? AND status = ? AND started_at IS NOT NULL AND finished_at IS NOT NULL", from, model.JobSucceeded).Order("created_at desc").Limit(5000).Find(&done)
	durs := map[string][]int64{}
	for _, j := range done {
		durs[j.Type] = append(durs[j.Type], j.FinishedAt.Sub(*j.StartedAt).Milliseconds())
	}
	jobs := []map[string]any{}
	for t, st := range jt {
		total := 0.0
		for _, n := range st {
			total += n
		}
		jobs = append(jobs, map[string]any{"type": t, "label": labelOf(jobLabels, t), "total": total, "done": st[model.JobSucceeded], "failed": st[model.JobFailed], "canceled": st[model.JobCanceled], "medianMs": median(durs[t])})
	}
	sort.Slice(jobs, func(i, j int) bool { return jobs[i]["total"].(float64) > jobs[j]["total"].(float64) })
	var errPaths []struct {
		Path string
		N    float64
	}
	m(&model.ErrorLog{}).Where("created_at > ?", from).Select("path, COUNT(*) AS n").Group("path").Order("n desc").Limit(8).Scan(&errPaths)
	errTop := []part{}
	for _, x := range errPaths {
		errTop = append(errTop, part{Key: x.Path, Label: x.Path, Value: x.N})
	}
	reliability := map[string]any{
		"jobs": jobs, "errors": period(&model.ErrorLog{}, ""), "errorPaths": errTop,
		"failedJobs": period(&model.Job{}, "status = ? AND parent_id = ''", model.JobFailed),
		"series": map[string]any{
			"errors": series(m(&model.ErrorLog{}).Where("created_at > ?", from), "COUNT(*)"),
			"failed": series(m(&model.Job{}).Where("created_at > ? AND status = ?", from, model.JobFailed), "COUNT(*)"),
		},
	}

	return map[string]any{"days": days, "dayList": dayList, "growth": growth, "revenue": revenue, "ai": ai, "production": production, "reliability": reliability}, nil
}

func median(v []int64) int64 {
	if len(v) == 0 {
		return 0
	}
	sort.Slice(v, func(i, j int) bool { return v[i] < v[j] })
	return v[len(v)/2]
}
