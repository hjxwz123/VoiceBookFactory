package core

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"voicebook/internal/ai/llm"
	"voicebook/internal/ai/tts"
	"voicebook/internal/model"
	"voicebook/internal/text"
)

// ---------- character analysis ----------

type CharacterInfo struct {
	Name        string   `json:"name"`
	Aliases     []string `json:"aliases"`
	Kind        string   `json:"kind"`
	Gender      string   `json:"gender"`
	AgeGroup    string   `json:"ageGroup"`
	Identity    string   `json:"identity"`
	Personality string   `json:"personality"`
	VoiceTraits string   `json:"voiceTraits"`
	Profile     string   `json:"profile"`
	Lines       int      `json:"lines"`
	chapter     int
}

var characterSchema = obj(map[string]any{
	"characters": arr(obj(map[string]any{
		"name": str(), "aliases": arr(str()), "kind": enum("lead", "supporting", "minor", "crowd"),
		"gender": enum("male", "female", "unknown"), "ageGroup": enum("child", "teen", "young", "adult", "middle", "senior", "unknown"),
		"identity": str(), "personality": str(), "voiceTraits": str(), "profile": str(), "lines": integer(),
	})),
})

var kindRank = map[string]int{"lead": 4, "supporting": 3, "minor": 2, "crowd": 1}

type textChunk struct {
	Text    string
	Chapter int
}

// sampleChunks picks representative text for analysis: everything if short, otherwise spread samples.
func sampleChunks(chapters []model.Chapter, chunkSize, budget int) []textChunk {
	total := 0
	for _, c := range chapters {
		total += utf8.RuneCountInString(c.SourceText)
	}
	var out []textChunk
	if total <= budget {
		for _, c := range chapters {
			r := []rune(c.SourceText)
			for i := 0; i < len(r); i += chunkSize {
				e := min(i+chunkSize, len(r))
				out = append(out, textChunk{string(r[i:e]), c.Index})
			}
		}
		return out
	}
	// first chapters fully (where most characters are introduced) then evenly spaced samples
	used := 0
	for _, c := range chapters {
		if used >= budget/2 {
			break
		}
		r := []rune(c.SourceText)
		for i := 0; i < len(r) && used < budget/2; i += chunkSize {
			e := min(i+chunkSize, len(r))
			out = append(out, textChunk{string(r[i:e]), c.Index})
			used += e - i
		}
	}
	remaining := budget - used
	n := remaining / chunkSize
	if n < 1 {
		return out
	}
	step := len(chapters) / (n + 1)
	if step < 1 {
		step = 1
	}
	for k := 1; k <= n && k*step < len(chapters); k++ {
		c := chapters[k*step]
		r := []rune(c.SourceText)
		e := min(chunkSize, len(r))
		out = append(out, textChunk{string(r[:e]), c.Index})
	}
	return out
}

func (a *App) AnalyzeCharacters(ctx context.Context, h *LLMHandle, chapters []model.Chapter, progress func(float64, string)) ([]CharacterInfo, int, error) {
	chunks := sampleChunks(chapters, 6000, 72000)
	merged := map[string]*CharacterInfo{}
	var order []string
	totalChars := 0
	find := func(ci CharacterInfo) *CharacterInfo {
		names := append([]string{ci.Name}, ci.Aliases...)
		for _, key := range order {
			m := merged[key]
			all := append([]string{m.Name}, m.Aliases...)
			for _, n := range names {
				for _, x := range all {
					if n != "" && n == x {
						return m
					}
				}
			}
		}
		return nil
	}
	for i, ch := range chunks {
		if ctx.Err() != nil {
			return nil, totalChars, ctx.Err()
		}
		progress(float64(i)/float64(len(chunks)), fmt.Sprintf("正在分析第 %d/%d 段文本", i+1, len(chunks)))
		known := ""
		if len(order) > 0 {
			var names []string
			for _, k := range order {
				names = append(names, merged[k].Name)
			}
			known = "\n\n已识别的角色（若片段中出现请使用相同名称）：" + strings.Join(names, "、")
		}
		var out struct {
			Characters []CharacterInfo `json:"characters"`
		}
		_, err := a.CallLLM(ctx, h, llm.Request{
			System:     a.promptFor(ctx, PromptCharacters),
			Messages:   []llm.Message{{Role: "user", Content: "小说片段：\n" + ch.Text + known}},
			Schema:     characterSchema,
			SchemaName: "characters",
			MaxTokens:  16000,
		}, &out)
		if err != nil {
			return nil, totalChars, err
		}
		totalChars += utf8.RuneCountInString(ch.Text)
		for _, c := range out.Characters {
			c.Name = strings.TrimSpace(c.Name)
			if c.Name == "" || c.Name == "旁白" || c.Name == "叙述者" {
				continue
			}
			c.chapter = ch.Chapter
			if m := find(c); m != nil {
				m.Lines += c.Lines
				if kindRank[c.Kind] > kindRank[m.Kind] {
					m.Kind = c.Kind
				}
				for _, al := range append(c.Aliases, c.Name) {
					if al != m.Name && !contains(m.Aliases, al) {
						m.Aliases = append(m.Aliases, al)
					}
				}
				if len(c.Profile) > len(m.Profile) {
					m.Profile = c.Profile
				}
				for _, f := range []struct {
					dst *string
					src string
				}{{&m.Gender, c.Gender}, {&m.AgeGroup, c.AgeGroup}, {&m.Identity, c.Identity}, {&m.Personality, c.Personality}, {&m.VoiceTraits, c.VoiceTraits}} {
					if (*f.dst == "" || *f.dst == "unknown") && f.src != "" {
						*f.dst = f.src
					}
				}
				continue
			}
			cc := c
			merged[c.Name] = &cc
			order = append(order, c.Name)
		}
	}
	var list []CharacterInfo
	for _, k := range order {
		list = append(list, *merged[k])
	}
	sort.SliceStable(list, func(i, j int) bool {
		if kindRank[list[i].Kind] != kindRank[list[j].Kind] {
			return kindRank[list[i].Kind] > kindRank[list[j].Kind]
		}
		return list[i].Lines > list[j].Lines
	})
	return list, totalChars, nil
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// ---------- script annotation ----------

type UnitLabel struct {
	ID          int      `json:"id"`
	Type        string   `json:"type"`
	Speaker     string   `json:"speaker"`
	Emotion     string   `json:"emotion"`
	Intensity   int      `json:"intensity"`
	Tags        []string `json:"tags"` // earlier prompt versions
	Cues        []LLMCue `json:"cues"`
	Instruction string   `json:"instruction"`
	Confidence  float64  `json:"confidence"`
	SpeechTag   bool     `json:"speechTag"`
	Prons       []struct {
		Word      string `json:"word"`
		Char      string `json:"char"`
		Pinyin    string `json:"pinyin"`
		Homophone string `json:"homophone"`
	} `json:"prons"`
}

type SfxCue struct {
	AfterID     int    `json:"afterId"`
	Description string `json:"description"`
}

var scriptSchema = obj(map[string]any{
	"units": arr(obj(map[string]any{
		"id": integer(), "type": enum("narration", "dialogue", "monologue"), "speaker": str(),
		"emotion":   enum("neutral", "happy", "sad", "angry", "fearful", "surprised", "disgusted", "gentle", "nervous", "tired", "whisper", "shout"),
		"intensity": integer(), "cues": arr(obj(map[string]any{"tag": enum(perfTagKeys()...), "after": str()})), "instruction": str(),
		"confidence": num(), "speechTag": boolean(),
		"prons": arr(obj(map[string]any{"word": str(), "char": str(), "pinyin": str(), "homophone": str()})),
	})),
	"sfx": arr(obj(map[string]any{"afterId": integer(), "description": str()})),
})

func perfTagKeys() []string {
	var out []string
	for _, t := range tts.PerformanceTags {
		out = append(out, t.Key)
	}
	return out
}

// perfTagGuide lists the cue vocabulary for the annotation prompt.
func perfTagGuide() string {
	var style, sound []string
	for _, t := range tts.PerformanceTags {
		item := t.Key + "（" + t.Label + "：" + t.Hint + "）"
		if t.Kind == "style" {
			style = append(style, item)
		} else {
			sound = append(sound, item)
		}
	}
	return "语气风格标签（作用于标签之后的内容，直到下一个风格标签）：" + strings.Join(style, "、") + "\n声音标签（在该位置插入一个非语言声音）：" + strings.Join(sound, "、")
}

type AnnotateResult struct {
	Labels map[int]UnitLabel
	Sfx    []SfxCue
	Chars  int
}

func describeCharacters(chars []model.Character) (string, map[string]string) {
	var sb strings.Builder
	ids := map[string]string{}
	n := 0
	for _, c := range chars {
		if c.Kind == model.CharNarrator {
			continue
		}
		n++
		code := fmt.Sprintf("C%d", n)
		ids[code] = c.ID
		g := map[string]string{"male": "男", "female": "女"}[c.Gender]
		fmt.Fprintf(&sb, "%s %s", code, c.Name)
		var meta []string
		if len(c.Aliases.V) > 0 {
			meta = append(meta, "别名："+strings.Join(c.Aliases.V, "、"))
		}
		if g != "" {
			meta = append(meta, g)
		}
		if c.Identity != "" {
			meta = append(meta, truncateStr(c.Identity, 30))
		}
		if len(meta) > 0 {
			sb.WriteString("（" + strings.Join(meta, "；") + "）")
		}
		sb.WriteString("\n")
	}
	if n == 0 {
		sb.WriteString("（暂无已知角色，请用 NEW:角色名 表示）\n")
	}
	return sb.String(), ids
}

func (a *App) AnnotateUnits(ctx context.Context, h *LLMHandle, units []text.Unit, chars []model.Character, drama bool, progress func(float64, string)) (*AnnotateResult, error) {
	charTable, _ := describeCharacters(chars)
	sfxRule := "10. sfx：返回空数组。"
	if drama {
		sfxRule = "10. sfx：这是广播剧，请在需要音效的位置给出音效提示（afterId 为音效紧跟其后的单元编号，description 为音效描述，如“推门声”“雨声渐大”），只标注对剧情有意义的音效，每 20 个单元最多 2 个。"
	}
	system := fill(a.promptFor(ctx, PromptScript), map[string]string{"sfx_rule": sfxRule, "tag_rule": perfTagGuide()})
	res := &AnnotateResult{Labels: map[int]UnitLabel{}}
	// batch units by length
	var batches [][]text.Unit
	var cur []text.Unit
	size := 0
	for _, u := range units {
		l := utf8.RuneCountInString(u.Text)
		if size+l > 2200 && len(cur) > 0 {
			batches = append(batches, cur)
			cur, size = nil, 0
		}
		cur = append(cur, u)
		size += l
	}
	if len(cur) > 0 {
		batches = append(batches, cur)
	}
	for bi, batch := range batches {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		progress(float64(bi)/float64(len(batches)), fmt.Sprintf("AI 正在标注第 %d/%d 批文本", bi+1, len(batches)))
		var sb strings.Builder
		sb.WriteString("【角色表】\n" + charTable)
		// context: last units of previous batch with their labels
		if bi > 0 {
			prev := batches[bi-1]
			start := max(0, len(prev)-4)
			sb.WriteString("\n【上文（已标注，仅供参考，不需要返回）】\n")
			for _, u := range prev[start:] {
				lb := res.Labels[u.Index]
				fmt.Fprintf(&sb, "[%d] (%s) %s\n", u.Index, lb.Speaker, u.Text)
			}
		}
		var batchText strings.Builder
		sb.WriteString("\n【需要标注的文本单元】\n")
		for _, u := range batch {
			kind := "叙述"
			if u.Quote {
				kind = "引号内"
			}
			fmt.Fprintf(&sb, "[%d] (%s) %s\n", u.Index, kind, u.Text)
			batchText.WriteString(u.Text)
		}
		if poly := text.PolyphonicsIn(batchText.String()); len(poly) > 0 {
			if len(poly) > 60 {
				poly = poly[:60]
			}
			sb.WriteString("\n【待注意多音字】" + strings.Join(poly, " ") + "\n")
		}
		fmt.Fprintf(&sb, "\n请返回编号 %d 到 %d 的全部 %d 个单元的标注。", batch[0].Index, batch[len(batch)-1].Index, len(batch))
		var out struct {
			Units []UnitLabel `json:"units"`
			Sfx   []SfxCue    `json:"sfx"`
		}
		_, err := a.CallLLM(ctx, h, llm.Request{System: system, Messages: []llm.Message{{Role: "user", Content: sb.String()}}, Schema: scriptSchema, SchemaName: "script", MaxTokens: 16000}, &out)
		if err != nil {
			return nil, err
		}
		res.Chars += utf8.RuneCountInString(batchText.String())
		valid := map[int]bool{}
		for _, u := range batch {
			valid[u.Index] = true
		}
		for _, l := range out.Units {
			if valid[l.ID] {
				res.Labels[l.ID] = l
			}
		}
		for _, s := range out.Sfx {
			if valid[s.AfterID] && strings.TrimSpace(s.Description) != "" {
				res.Sfx = append(res.Sfx, s)
			}
		}
	}
	progress(1, "标注完成")
	return res, nil
}

// ---------- casting ----------

var castingSchema = obj(map[string]any{
	"castings": arr(obj(map[string]any{
		"characterId": str(),
		"candidates":  arr(obj(map[string]any{"voiceId": str(), "score": num(), "reason": str()})),
	})),
})

func voiceLine(code string, v model.Voice) string {
	g := map[string]string{"male": "男", "female": "女"}[v.Gender]
	return fmt.Sprintf("%s %s（%s；%s；%s；%s）", code, v.Name, g, ageName(v.AgeGroup), strings.Join(v.Tags.V, "、"), truncateStr(v.Description, 40))
}

func ageName(a string) string {
	return map[string]string{"child": "儿童", "teen": "少年", "young": "青年", "adult": "成年", "middle": "中年", "senior": "老年"}[a]
}

// RecommendVoices ranks candidate voices for characters with the LLM.
func (a *App) RecommendVoices(ctx context.Context, h *LLMHandle, chars []model.Character, pool []model.Voice) (map[string][]model.CastingCandidate, int, error) {
	vcode := map[string]string{}
	var vlines []string
	for i, v := range pool {
		code := fmt.Sprintf("V%d", i+1)
		vcode[code] = v.ID
		vlines = append(vlines, voiceLine(code, v))
	}
	out := map[string][]model.CastingCandidate{}
	total := 0
	for start := 0; start < len(chars); start += 12 {
		end := min(start+12, len(chars))
		var sb strings.Builder
		ccode := map[string]string{}
		sb.WriteString("【角色】\n")
		for i, c := range chars[start:end] {
			code := fmt.Sprintf("C%d", start+i+1)
			ccode[code] = c.ID
			kind := map[string]string{"narrator": "旁白", "lead": "主角", "supporting": "重要配角", "minor": "次要角色", "crowd": "群演"}[c.Kind]
			fmt.Fprintf(&sb, "%s %s（%s；%s；%s）性格：%s；声音：%s；小传：%s\n", code, c.Name, kind, map[string]string{"male": "男", "female": "女"}[c.Gender], ageName(c.AgeGroup), truncateStr(c.Personality, 40), truncateStr(c.VoiceTraits, 50), truncateStr(c.Profile, 80))
		}
		sb.WriteString("\n【候选音色】\n" + strings.Join(vlines, "\n"))
		var res struct {
			Castings []struct {
				CharacterID string `json:"characterId"`
				Candidates  []struct {
					VoiceID string  `json:"voiceId"`
					Score   float64 `json:"score"`
					Reason  string  `json:"reason"`
				} `json:"candidates"`
			} `json:"castings"`
		}
		_, err := a.CallLLM(ctx, h, llm.Request{System: a.Prompt(PromptCasting), Messages: []llm.Message{{Role: "user", Content: sb.String()}}, Schema: castingSchema, SchemaName: "casting", MaxTokens: 12000}, &res)
		if err != nil {
			return nil, total, err
		}
		total += utf8.RuneCountInString(sb.String())
		for _, cs := range res.Castings {
			cid, ok := ccode[cs.CharacterID]
			if !ok {
				continue
			}
			for _, cand := range cs.Candidates {
				if vid, ok := vcode[cand.VoiceID]; ok {
					out[cid] = append(out[cid], model.CastingCandidate{VoiceID: vid, Score: cand.Score, Reason: cand.Reason})
				}
			}
		}
	}
	return out, total, nil
}

// HeuristicCasting scores voices by gender, age, tags and tier (used when no LLM is configured).
func HeuristicCasting(c model.Character, pool []model.Voice, preferTier string) []model.CastingCandidate {
	return heuristicTop(c, pool, preferTier, 5)
}

// CastingShortlist keeps the best rule-ranked voices per character so the LLM prompt stays small
// even when the library holds thousands of voices.
func CastingShortlist(chars []model.Character, pool []model.Voice, prefer func(kind string) string, perChar, max int) []model.Voice {
	byID := map[string]model.Voice{}
	for _, v := range pool {
		byID[v.ID] = v
	}
	seen := map[string]bool{}
	var out []model.Voice
	for round := 0; round < perChar && len(out) < max; round++ {
		for _, c := range chars {
			top := heuristicTop(c, pool, prefer(c.Kind), perChar)
			if round < len(top) && !seen[top[round].VoiceID] && len(out) < max {
				seen[top[round].VoiceID] = true
				out = append(out, byID[top[round].VoiceID])
			}
		}
	}
	return out
}

var audiobookTags = []string{"有声书", "有声阅读", "有声书配音", "古风有声书", "旁白", "动漫配音", "角色音", "配音讲解"}

func heuristicTop(c model.Character, pool []model.Voice, preferTier string, limit int) []model.CastingCandidate {
	ages := []string{"child", "teen", "young", "adult", "middle", "senior"}
	ai := func(s string) int {
		for i, x := range ages {
			if x == s {
				return i
			}
		}
		return 3
	}
	type sc struct {
		v model.Voice
		s float64
		r []string
	}
	var list []sc
	for _, v := range pool {
		s := 0.4
		var reasons []string
		if c.Gender != "" && c.Gender != "unknown" {
			if v.Gender == c.Gender {
				s += 0.3
				reasons = append(reasons, "性别匹配")
			} else {
				s -= 0.5
			}
		}
		d := ai(v.AgeGroup) - ai(c.AgeGroup)
		if d < 0 {
			d = -d
		}
		s += 0.2 - 0.07*float64(d)
		if d == 0 {
			reasons = append(reasons, "年龄段一致")
		}
		desc := c.VoiceTraits + c.Personality + c.Profile
		for _, t := range v.Tags.V {
			if t != "" && strings.Contains(desc, t) {
				s += 0.08
				reasons = append(reasons, "风格「"+t+"」")
			}
		}
		if c.Kind == model.CharNarrator && (contains(v.Tags.V, "旁白") || contains(v.Tags.V, "播音腔") || contains(v.Tags.V, "有声书")) {
			s += 0.25
			reasons = append(reasons, "适合长篇旁白")
		}
		for _, t := range audiobookTags {
			if contains(v.Tags.V, t) {
				s += 0.08
				if c.Kind != model.CharNarrator {
					reasons = append(reasons, "适合有声演播")
				}
				break
			}
		}
		if v.Provider == "placeholder" {
			s -= 0.3 // demo engine voices only win when no real platform is connected
		}
		if preferTier != "" {
			if v.PriceTier == preferTier {
				s += 0.12
			} else {
				s -= 0.04
			}
		}
		list = append(list, sc{v, s, reasons})
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].s > list[j].s })
	var out []model.CastingCandidate
	for i := 0; i < len(list) && i < limit; i++ {
		r := "综合匹配"
		if len(list[i].r) > 0 {
			r = strings.Join(list[i].r, "，")
		}
		score := list[i].s
		if score > 1 {
			score = 1
		}
		if score < 0 {
			score = 0
		}
		out = append(out, model.CastingCandidate{VoiceID: list[i].v.ID, Score: float64(int(score*100)) / 100, Reason: r + "（规则匹配）"})
	}
	return out
}

// ---------- lexicon, voice text, sfx, moderation, misc ----------

type LexTerm struct {
	Term   string `json:"term"`
	Pinyin string `json:"pinyin"`
	Note   string `json:"note"`
}

func (a *App) ExtractLexicon(ctx context.Context, h *LLMHandle, sample string, names []string) ([]LexTerm, int, error) {
	var out struct {
		Terms []LexTerm `json:"terms"`
	}
	content := "角色名：" + strings.Join(names, "、") + "\n\n文本：\n" + sample
	_, err := a.CallLLM(ctx, h, llm.Request{System: a.Prompt(PromptLexicon), Messages: []llm.Message{{Role: "user", Content: content}},
		Schema: obj(map[string]any{"terms": arr(obj(map[string]any{"term": str(), "pinyin": str(), "note": str()}))}), SchemaName: "lexicon", MaxTokens: 8000}, &out)
	return out.Terms, utf8.RuneCountInString(content), err
}

func (a *App) DescribeVoice(ctx context.Context, h *LLMHandle, c model.Character) (string, error) {
	var out struct {
		Description string `json:"description"`
	}
	content := fmt.Sprintf("角色：%s\n性别：%s\n年龄段：%s\n身份：%s\n性格：%s\n声音特征：%s\n小传：%s", c.Name, c.Gender, ageName(c.AgeGroup), c.Identity, c.Personality, c.VoiceTraits, c.Profile)
	_, err := a.CallLLM(ctx, h, llm.Request{System: a.Prompt(PromptVoiceDesc), Messages: []llm.Message{{Role: "user", Content: content}}, Schema: obj(map[string]any{"description": str()}), SchemaName: "voice_description", MaxTokens: 4000, Effort: "low"}, &out)
	return out.Description, err
}

type VoiceAttrs struct {
	Gender   string   `json:"gender"`
	AgeGroup string   `json:"ageGroup"`
	Tags     []string `json:"tags"`
	Name     string   `json:"name"`
}

func (a *App) ParseVoiceAttributes(ctx context.Context, h *LLMHandle, desc string) (VoiceAttrs, error) {
	var out VoiceAttrs
	_, err := a.CallLLM(ctx, h, llm.Request{System: a.Prompt(PromptVoiceAttrs), Messages: []llm.Message{{Role: "user", Content: desc}},
		Schema:     obj(map[string]any{"gender": enum("male", "female", "unknown"), "ageGroup": enum("child", "teen", "young", "adult", "middle", "senior", "unknown"), "tags": arr(str()), "name": str()}),
		SchemaName: "voice_attributes", MaxTokens: 3000, Effort: "low"}, &out)
	return out, err
}

// RuleVoiceAttributes is a keyword fallback for voice attributes.
func RuleVoiceAttributes(desc string) VoiceAttrs {
	v := VoiceAttrs{Gender: "unknown", AgeGroup: "adult"}
	switch {
	case strings.ContainsAny(desc, "女姑娘妹姐妈奶婆"):
		v.Gender = "female"
	case strings.ContainsAny(desc, "男哥叔爷爸公"):
		v.Gender = "male"
	}
	switch {
	case strings.Contains(desc, "孩") || strings.Contains(desc, "童") || strings.Contains(desc, "儿童"):
		v.AgeGroup = "child"
	case strings.Contains(desc, "少年") || strings.Contains(desc, "少女") || strings.Contains(desc, "十几"):
		v.AgeGroup = "teen"
	case strings.Contains(desc, "青年") || strings.Contains(desc, "二十"):
		v.AgeGroup = "young"
	case strings.Contains(desc, "中年") || strings.Contains(desc, "四十") || strings.Contains(desc, "五十"):
		v.AgeGroup = "middle"
	case strings.Contains(desc, "老") || strings.Contains(desc, "苍"):
		v.AgeGroup = "senior"
	}
	for _, t := range []string{"清亮", "温柔", "低沉", "沙哑", "磁性", "甜美", "沉稳", "活泼", "冷峻", "温暖", "清冷", "浑厚", "播音腔", "少年感", "御姐", "知性"} {
		if strings.Contains(desc, t) {
			v.Tags = append(v.Tags, t)
		}
	}
	v.Name = "自定义音色"
	return v
}

type SfxChoice struct {
	AssetID string `json:"assetId"`
	Prompt  string `json:"prompt"`
}

func (a *App) MatchSFX(ctx context.Context, h *LLMHandle, cue string, assets []model.Asset) (SfxChoice, error) {
	var sb strings.Builder
	codes := map[string]string{}
	for i, as := range assets {
		code := fmt.Sprintf("A%d", i+1)
		codes[code] = as.ID
		fmt.Fprintf(&sb, "%s %s（%s）\n", code, as.Name, strings.Join(as.Tags.V, "、"))
	}
	var out struct {
		AssetCode string `json:"assetCode"`
		Prompt    string `json:"prompt"`
	}
	_, err := a.CallLLM(ctx, h, llm.Request{System: a.Prompt(PromptSFX) + "\n如果选择素材，assetCode 填素材编号；否则 assetCode 为空字符串并填写 prompt。",
		Messages: []llm.Message{{Role: "user", Content: "音效提示：" + cue + "\n\n候选音效：\n" + sb.String()}},
		Schema:   obj(map[string]any{"assetCode": str(), "prompt": str()}), SchemaName: "sfx", MaxTokens: 2000, Effort: "low"}, &out)
	return SfxChoice{AssetID: codes[out.AssetCode], Prompt: out.Prompt}, err
}

type ModerationVerdict struct {
	Verdict    string   `json:"verdict"`
	Categories []string `json:"categories"`
	Reason     string   `json:"reason"`
}

func (a *App) ModerateLLM(ctx context.Context, h *LLMHandle, content string) (ModerationVerdict, error) {
	var out ModerationVerdict
	_, err := a.CallLLM(ctx, h, llm.Request{System: a.Prompt(PromptModerate), Messages: []llm.Message{{Role: "user", Content: content}},
		Schema: obj(map[string]any{"verdict": enum("pass", "review", "block"), "categories": arr(str()), "reason": str()}), SchemaName: "moderation", MaxTokens: 2000, Effort: "low"}, &out)
	return out, err
}

func (a *App) DetectChapterLines(ctx context.Context, h *LLMHandle, lines []string) ([]int, error) {
	var sb strings.Builder
	for i, l := range lines {
		fmt.Fprintf(&sb, "%d: %s\n", i, l)
	}
	var out struct {
		Lines []int `json:"lines"`
	}
	_, err := a.CallLLM(ctx, h, llm.Request{System: a.Prompt(PromptChapters), Messages: []llm.Message{{Role: "user", Content: sb.String()}},
		Schema: obj(map[string]any{"lines": arr(integer())}), SchemaName: "chapters", MaxTokens: 6000, Effort: "low"}, &out)
	return out.Lines, err
}

type BookIntro struct {
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
	Genre       string   `json:"genre"`
}

func (a *App) GenerateBookIntro(ctx context.Context, h *LLMHandle, title, sample string) (BookIntro, error) {
	var out BookIntro
	_, err := a.CallLLM(ctx, h, llm.Request{System: a.Prompt(PromptBookIntro), Messages: []llm.Message{{Role: "user", Content: "书名：" + title + "\n\n开头内容：\n" + sample}},
		Schema: obj(map[string]any{"description": str(), "tags": arr(str()), "genre": str()}), SchemaName: "book_intro", MaxTokens: 4000, Effort: "low"}, &out)
	return out, err
}

func (a *App) CoverPrompt(ctx context.Context, h *LLMHandle, b model.Book) (string, error) {
	var out struct {
		Prompt string `json:"prompt"`
	}
	_, err := a.CallLLM(ctx, h, llm.Request{System: a.Prompt(PromptCover), Messages: []llm.Message{{Role: "user", Content: fmt.Sprintf("书名：%s\n题材：%s\n简介：%s", b.Title, b.Genre, b.Description)}},
		Schema: obj(map[string]any{"prompt": str()}), SchemaName: "cover", MaxTokens: 2000, Effort: "low"}, &out)
	return out.Prompt, err
}
