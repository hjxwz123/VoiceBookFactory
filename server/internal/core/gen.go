package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"voicebook/internal/ai/gen"
	"voicebook/internal/ai/llm"
	dbx "voicebook/internal/db"
	"voicebook/internal/jobs"
	"voicebook/internal/model"
)

func deleteDir(d string) error { return os.RemoveAll(d) }

var sfxKindNames = map[string]string{
	"rain": "雨声", "thunder": "雷声", "wind": "风声", "knock": "敲门", "door": "开门", "footsteps": "脚步", "heartbeat": "心跳", "bell": "钟声",
	"clock": "钟表", "fire": "篝火", "water": "流水", "waves": "海浪", "birds": "鸟鸣", "phone": "电话", "sword": "兵器", "explosion": "爆炸",
	"hooves": "马蹄", "crowd": "人群", "applause": "掌声", "whoosh": "转场",
}

var moodNames = map[string]string{"calm": "舒缓", "happy": "轻快", "sad": "伤感", "suspense": "悬疑", "epic": "史诗", "mystic": "神秘"}

// GenRequest is one AI generation order from the studio.
type GenRequest struct {
	Kind       string `json:"kind"` // sfx | music | image
	Prompt     string `json:"prompt"`
	DurationMs int    `json:"durationMs"` // 0: the service picks a length that suits the description (ElevenLabs)
	Loop       bool   `json:"loop"`       // sound effects: one that repeats seamlessly (ambience)
	Count      int    `json:"count"`
	BookID     string `json:"bookId"`
	SetCover   bool   `json:"setCover"`
	NoTitle    bool   `json:"noTitle"` // book covers: keep the title off the picture
	// music from providers that make songs (Suno): sung rather than instrumental, own lyrics, style, title
	Vocal  bool   `json:"vocal"`
	Lyrics string `json:"lyrics"`
	Style  string `json:"style"`
	Title  string `json:"title"`
	// sound effects from services that imitate a sound (Volcengine): up to three audio assets as reference
	// recordings ("提示音"), named @音频1… in the description
	References []string `json:"references"`
}

// sfxReferenceLimit is how many reference recordings a sound effect service takes (0: none).
func sfxReferenceLimit(provider string) int {
	if provider == "volcengine_audio" {
		return 3
	}
	return 0
}

// GenOptions tells the studio's generate form what the configured services can do.
func (a *App) GenOptions(studioID string) map[string]any {
	out := map[string]any{}
	for _, kind := range []string{model.KindMusic, model.KindSFX, model.KindImage} {
		acc, _, err := a.genAccount(kind, studioID)
		if err != nil {
			out[kind] = map[string]any{"available": false}
			continue
		}
		v := map[string]any{"available": true, "provider": acc.Provider, "builtin": acc.Provider == "placeholder"}
		if kind == model.KindSFX && acc.Provider == "elevenlabs_sfx" {
			v["name"], v["maxSeconds"], v["loop"], v["autoDuration"] = "ElevenLabs", 30, true, true
		}
		if kind == model.KindSFX && acc.Provider == "volcengine_audio" {
			v["name"], v["maxSeconds"], v["autoDuration"], v["references"], v["chinese"] = "火山引擎", gen.VolcSFXMaxSeconds, true, sfxReferenceLimit(acc.Provider), true
		}
		if kind == model.KindMusic {
			v["features"] = gen.MusicFeaturesOf(acc.Provider)
			if acc.Provider == "suno" {
				v["name"] = "Suno"
			}
		}
		out[kind] = v
	}
	return out
}

// EnqueueGenerate starts an AI generation job for sfx | music | image.
func (a *App) EnqueueGenerate(x *Access, in GenRequest) (*model.Job, error) {
	kind, prompt, count := in.Kind, strings.TrimSpace(in.Prompt), in.Count
	if prompt == "" && kind != model.KindImage {
		return nil, Bad("请描述想要生成的内容")
	}
	if kind == model.KindImage && in.BookID == "" && prompt == "" {
		return nil, Bad("请描述想要的画面")
	}
	acc, _, err := a.genAccount(kind, x.StudioID)
	if err != nil {
		return nil, err
	}
	if kind == model.KindMusic {
		f := gen.MusicFeaturesOf(acc.Provider)
		if !f.Vocals {
			in.Vocal = false
		}
		if !f.Lyrics || !in.Vocal { // an instrumental sings no lyrics
			in.Lyrics = ""
		}
		if !f.Style {
			in.Style = ""
		}
		switch {
		case len([]rune(in.Lyrics)) > 3000:
			return nil, Bad("歌词太长了，最多 3000 字")
		case len([]rune(in.Style)) > 200:
			return nil, Bad("风格描述太长了，最多 200 字")
		case len([]rune(in.Title)) > 80:
			return nil, Bad("标题太长了，最多 80 字")
		}
	}
	feature := map[string]string{model.KindSFX: FeatSFX, model.KindMusic: FeatMusic, model.KindImage: FeatImage}[kind]
	if feature == "" {
		return nil, Bad("不支持的生成类型")
	}
	var refs []string
	if kind == model.KindSFX && len(in.References) > 0 {
		limit := sfxReferenceLimit(acc.Provider)
		if limit == 0 {
			return nil, Bad("当前的音效服务不支持参考音频")
		}
		if len(in.References) > limit {
			return nil, Bad("最多选 %d 段参考音频", limit)
		}
		for _, id := range in.References {
			var as model.Asset
			if a.DB.First(&as, "id = ?", id).Error != nil || !a.assetUsableBy(x, &as) {
				return nil, Bad("找不到选中的参考音频")
			}
			if as.DurationMs <= 0 && !strings.HasPrefix(as.Mime, "audio/") {
				return nil, Bad("「%s」不是音频", as.Name)
			}
			refs = append(refs, as.ID)
		}
	}
	if count <= 0 {
		count = map[string]int{model.KindSFX: 4, model.KindMusic: 1, model.KindImage: 4}[kind]
	}
	if count > 6 {
		count = 6
	}
	est := a.Estimate(feature, "", float64(count))
	if b := a.Balance(x.StudioID); b.Available < est {
		return nil, PaymentRequired(fmt.Sprintf("积分不足：需要 %d 积分", est), map[string]any{"need": est, "available": b.Available})
	}
	title := map[string]string{model.KindSFX: "AI 音效", model.KindMusic: "AI 配乐", model.KindImage: "AI 图片"}[kind] + " · " + clip(prompt, 20)
	if kind == model.KindImage && in.BookID != "" {
		var b model.Book
		a.DB.Select("title").First(&b, "id = ?", in.BookID)
		title = "AI 封面 · 《" + clip(b.Title, 20) + "》"
	}
	j := &model.Job{Type: kind + "_generate", StudioID: x.StudioID, BookID: in.BookID, RefType: "generate", Title: title, CreatedBy: x.User.ID, MaxAttempts: 1,
		Payload: model.Map{V: map[string]any{"prompt": prompt, "durationMs": max(in.DurationMs, 0), "loop": in.Loop && kind == model.KindSFX, "count": count, "setCover": in.SetCover, "noTitle": in.NoTitle,
			"vocal": in.Vocal, "lyrics": strings.TrimSpace(in.Lyrics), "style": strings.TrimSpace(in.Style), "title": strings.TrimSpace(in.Title)}}}
	if len(refs) > 0 {
		j.Payload.V["references"] = refs
	}
	return j, a.Q.Enqueue(j)
}

// assetUsableBy: official library assets, the user's own personal assets and the studio's assets.
func (a *App) assetUsableBy(x *Access, as *model.Asset) bool {
	switch {
	case as.Scope == model.ScopeSystem:
		return true
	case as.Scope == model.ScopeUser && as.StudioID == "":
		return as.OwnerUserID == x.User.ID
	}
	return as.StudioID == x.StudioID
}

// sfxReferences cuts each reference asset to the first 30 seconds of mono WAV, the clip size the service takes.
func (a *App) sfxReferences(ctx context.Context, ids []string) ([][]byte, func(), error) {
	if len(ids) == 0 {
		return nil, func() {}, nil
	}
	dir, err := a.Store.TempDir("sfx-ref")
	if err != nil {
		return nil, nil, err
	}
	done := func() { os.RemoveAll(dir) }
	var out [][]byte
	for i, id := range ids {
		p, as, err := a.AssetPath(id)
		if err != nil {
			done()
			return nil, nil, fmt.Errorf("参考音频不存在了，请重新选择")
		}
		clipPath := filepath.Join(dir, fmt.Sprintf("ref%d.wav", i))
		if err := a.FF.Cut(ctx, p, clipPath, 0, 29_500, 1, ""); err != nil {
			done()
			return nil, nil, fmt.Errorf("读取参考音频「%s」失败：%w", as.Name, err)
		}
		b, err := os.ReadFile(clipPath)
		if err != nil {
			done()
			return nil, nil, err
		}
		out = append(out, b)
	}
	return out, done, nil
}

// coverImagePrompt turns a scene idea into a full brief for a real image model. Audiobook stores
// show covers square, and this app crops them to 3:4 in lists, so the title stays in a centre safe area.
func coverImagePrompt(b model.Book, scene string, withTitle bool) string {
	var sb strings.Builder
	sb.WriteString("为一本有声书设计正方形（1:1）封面插画。\n画面：")
	sb.WriteString(strings.TrimSpace(scene))
	if b.Genre != "" {
		sb.WriteString("\n题材：" + b.Genre)
	}
	if withTitle && strings.TrimSpace(b.Title) != "" {
		fmt.Fprintf(&sb, "\n文字：用醒目、易读、与题材气质相称的字体排版书名「%s」", strings.TrimSpace(b.Title))
		if a := strings.TrimSpace(b.Author); a != "" {
			fmt.Fprintf(&sb, "，并用小一号的字写上作者「%s」", a)
		}
		sb.WriteString("。书名每个字都必须准确，不能有错别字或多余笔画；除此之外不要出现任何文字。")
	} else {
		sb.WriteString("\n画面中不要出现任何文字、字母或数字。")
	}
	sb.WriteString("\n构图：主体和文字集中在画面中央约四分之三宽度的范围内，左右留出安全边距，因为各平台会把封面裁成不同比例。不要水印、标志、边框或二维码。在手机上缩小展示时主体依然清晰，色彩有层次。")
	return sb.String()
}

func avatarImagePrompt(desc string) string {
	return "角色头像：" + strings.TrimSpace(desc) + "。半身肖像，正方形构图，人物居中，面部清晰、表情符合性格，背景简洁柔和，插画风格。画面里不要出现任何文字或水印。"
}

// endUserID is a stable, non-reversible id the image vendors use to trace abuse to one account
// without ever seeing who it is (OpenAI's safety_identifier).
func endUserID(userID string) string {
	sum := sha256.Sum256([]byte("voicebook:" + userID))
	return "vb_" + hex.EncodeToString(sum[:12])
}

// sfxPromptForProvider rewrites a Chinese description into the short English prompt sound effect models
// follow best (action, material, setting, distance). Without a language model, or when the account sets
// translate=false, the description is sent as written.
func (a *App) sfxPromptForProvider(ctx context.Context, studioID, prompt string, acc *model.ProviderAccount) string {
	if v, ok := acc.Config.V["translate"].(bool); ok && !v {
		return prompt
	}
	han := false
	for _, r := range prompt {
		if unicode.Is(unicode.Han, r) {
			han = true
			break
		}
	}
	if !han {
		return prompt
	}
	h, err := a.LLMFor(FeatSFX, "", studioID)
	if err != nil {
		return prompt
	}
	var out struct {
		Prompt string `json:"prompt"`
	}
	_, err = a.CallLLM(ctx, h, llm.Request{
		System: "You write prompts for a text-to-sound-effects model. Rewrite the user's description (usually Chinese) as one concise English prompt: " +
			"describe the sound source, the action, the material, the space and distance, and the mood, in order of what happens. Use audio terms such as " +
			"ambience, impact, whoosh, footsteps, close-mic, distant, reverb when they fit. No music unless asked, no speech, no numbers of repetitions. Under 40 words.",
		Messages: []llm.Message{{Role: "user", Content: prompt}},
		Schema:   obj(map[string]any{"prompt": str()}), SchemaName: "sfx_prompt", MaxTokens: 400, Effort: "low",
	}, &out)
	if p := strings.TrimSpace(out.Prompt); err == nil && p != "" {
		return truncateStr(p, 450)
	}
	return prompt
}

func (a *App) jobGenerate(kind string) jobs.Handler {
	return func(ctx context.Context, jc *jobs.Context) error {
		j := jc.Job
		prompt, _ := j.Payload.V["prompt"].(string)
		dur := intFrom(j.Payload.V["durationMs"])
		count := intFrom(j.Payload.V["count"])
		if count <= 0 {
			count = 1
		}
		acc, byok, err := a.genAccount(kind, j.StudioID)
		if err != nil {
			return jobs.Permanent(err)
		}
		feature := map[string]string{model.KindSFX: FeatSFX, model.KindMusic: FeatMusic, model.KindImage: FeatImage}[kind]
		var assetIDs []string
		var audioMs int64 // length of what was generated, for the cost of services billed by duration
		save := func(f *gen.File, name, assetKind string, tags []string, meta map[string]any) error {
			license := "commercial"
			if p := meta["provider"]; p == "suno" || p == "elevenlabs_sfx" || p == "volcengine_audio" { // a third-party service: its own terms decide whether the result may be used commercially
				license = "provider_terms"
			}
			var kept []string
			for _, t := range tags {
				if t != "" {
					kept = append(kept, t)
				}
			}
			as, err := a.SaveAssetBytes(ctx, AssetInput{StudioID: j.StudioID, OwnerUserID: j.CreatedBy, Kind: assetKind, Name: name, Ext: f.Ext, Mime: f.Mime, Origin: "ai", License: license, Tags: kept, CreatedBy: j.CreatedBy, Description: prompt, Meta: meta}, f.Data)
			if err != nil {
				return err
			}
			audioMs += as.DurationMs
			assetIDs = append(assetIDs, as.ID)
			return nil
		}
		charge := func(n int) error {
			c := Charge{StudioID: j.StudioID, UserID: j.CreatedBy, BookID: j.BookID, JobID: j.ID, Feature: feature, Quantity: float64(n), Unit: "calls", Provider: acc.Provider, AccountID: acc.ID, BYOK: byok}
			if kind == model.KindSFX && acc.Provider != "placeholder" {
				c.CostCNY = float64(audioMs) / 60000 * acc.CostPerUnit // sound effect services charge by the second
				c.Noted = true                                         // each request was counted as it was made
			}
			_, err := a.ChargeCredits(c)
			return err
		}
		switch kind {
		case model.KindSFX:
			p, err := gen.NewSFX(a.genAcc(acc))
			if err != nil {
				return jobs.Permanent(err)
			}
			loop, _ := j.Payload.V["loop"].(bool)
			real := acc.Provider != "placeholder"
			if !real && dur <= 0 {
				dur = 4000
			}
			sent := prompt
			if acc.Provider == "elevenlabs_sfx" {
				jc.Progress(0.02, "正在整理音效描述")
				sent = a.sfxPromptForProvider(ctx, j.StudioID, prompt, acc)
			}
			var refIDs []string
			if raw, ok := j.Payload.V["references"].([]any); ok {
				for _, id := range raw {
					refIDs = append(refIDs, fmt.Sprint(id))
				}
			}
			refs, cleanupRefs, err := a.sfxReferences(ctx, refIDs)
			if err != nil {
				return jobs.Permanent(err)
			}
			defer cleanupRefs()
			for i := 0; i < count; i++ {
				jc.Progress(float64(i)/float64(count)*0.95+0.05, fmt.Sprintf("正在生成第 %d/%d 个候选", i+1, count))
				d := dur
				if !real { // the built-in generator varies the length to give different takes
					d = dur + i*700
				}
				start := time.Now()
				f, err := p.GenerateSFX(ctx, gen.SFXRequest{Prompt: sent, DurationMs: d, Loop: loop, Seed: fmt.Sprint(j.ID, i), References: refs})
				if real {
					a.noteCall(acc.ID, time.Since(start), err)
				}
				if err != nil {
					if i > 0 { // keep the candidates already made and charge only for them
						slog.Warn("sfx candidate failed", "job", j.ID, "err", err)
						count = i
						break
					}
					return jobs.Permanent(withDetail("音效生成失败", err))
				}
				if sent != prompt {
					f.Meta["providerPrompt"] = sent
				}
				label := sfxKindNames[fmt.Sprint(f.Meta["kind"])]
				if loop {
					label = "可循环"
				}
				if err := save(f, fmt.Sprintf("%s %d", clip(prompt, 16), i+1), model.AssetSFX, []string{"AI 生成", label}, f.Meta); err != nil {
					return err
				}
			}
			if err := charge(count); err != nil {
				return jobs.Permanent(err)
			}
		case model.KindMusic:
			p, err := gen.NewMusic(a.genAcc(acc))
			if err != nil {
				return jobs.Permanent(err)
			}
			if dur <= 0 {
				dur = 60000
			}
			vocal, _ := j.Payload.V["vocal"].(bool)
			lyrics, _ := j.Payload.V["lyrics"].(string)
			style, _ := j.Payload.V["style"].(string)
			songTitle, _ := j.Payload.V["title"].(string)
			req := gen.MusicRequest{Prompt: prompt, DurationMs: dur, Instrumental: !vocal, Loopable: !vocal, Title: songTitle, Lyrics: lyrics, Style: style}
			if set, ok := p.(gen.MusicSetProvider); ok {
				// a song service (Suno): each request makes several tracks; keep them all as candidates
				per := gen.MusicFeaturesOf(acc.Provider).PerRequest
				if per <= 0 {
					per = 1
				}
				requests := (count + per - 1) / per
				var files []gen.File
				var lastErr error
				var taskIDs []string
				for i := 0; i < requests; i++ {
					jc.Progress(float64(i)/float64(requests)*0.9+0.05, fmt.Sprintf("正在用 %s 作曲（第 %d/%d 批），通常需要 1–3 分钟", acc.Name, i+1, requests))
					r := req
					r.Seed = fmt.Sprint(j.ID, i)
					r.OnTask = func(id string) { // kept on the job so an operator can find the task at the service
						taskIDs = append(taskIDs, id)
						j.Payload.V["taskIds"] = taskIDs
						a.DB.Model(&model.Job{}).Where("id = ?", j.ID).Update("payload", j.Payload)
					}
					start := time.Now()
					got, err := set.GenerateMusicSet(ctx, r)
					a.noteCall(acc.ID, time.Since(start), err)
					if err != nil {
						lastErr = err
						break
					}
					files = append(files, got...)
				}
				if len(files) == 0 {
					return jobs.Permanent(lastErr)
				}
				jc.Progress(0.95, "正在保存到资产库")
				for i := range files {
					f := &files[i]
					name := strings.TrimSpace(fmt.Sprint(f.Meta["title"]))
					if name == "" || name == "<nil>" {
						name = clip(prompt, 20)
						if songTitle != "" {
							name = clip(songTitle, 20)
						}
					}
					if len(files) > 1 {
						name = fmt.Sprintf("%s %d", name, i+1)
					}
					tags := []string{"AI 生成", "Suno", map[bool]string{true: "歌曲", false: "纯音乐"}[vocal]}
					meta := map[string]any{"prompt": prompt}
					for k, v := range f.Meta {
						meta[k] = v
					}
					if err := save(f, name, model.AssetBGM, tags, meta); err != nil {
						return err
					}
				}
				// the user asked for `count` tracks and saw that estimate; extras from the service are free
				charged := count
				if len(files) < count {
					charged = len(files)
				}
				if err := charge(charged); err != nil {
					return jobs.Permanent(err)
				}
				jc.SetResult(map[string]any{"assetIds": assetIDs, "prompt": prompt})
				msg := fmt.Sprintf("已生成 %d 首", len(files))
				if lastErr != nil {
					msg += fmt.Sprintf("，另有一批没有生成成功（未扣积分）：%v", lastErr)
				}
				jc.Progress(1, msg)
				return nil
			}
			for i := 0; i < count; i++ {
				jc.Progress(float64(i)/float64(count), "正在作曲")
				r := req
				r.Seed = fmt.Sprint(j.ID, i)
				f, err := p.GenerateMusic(ctx, r)
				if err != nil {
					return jobs.Permanent(withDetail("配乐生成失败", err))
				}
				mood := moodNames[fmt.Sprint(f.Meta["mood"])]
				if err := save(f, clip(prompt, 20), model.AssetBGM, []string{"AI 生成", mood, "可循环"}, f.Meta); err != nil {
					return err
				}
			}
			if err := charge(count); err != nil {
				return jobs.Permanent(err)
			}
		case model.KindImage:
			p, err := gen.NewImage(a.genAcc(acc))
			if err != nil {
				return jobs.Permanent(err)
			}
			real := acc.Provider != "placeholder" // the placeholder paints abstract art and cannot letter a title
			chID, _ := j.Payload.V["characterId"].(string)
			isCover := j.BookID != "" && chID == ""
			var b model.Book
			if isCover {
				a.DB.First(&b, "id = ?", j.BookID)
				if prompt == "" {
					jc.Progress(0.05, "正在根据书名和简介构思画面")
					prompt = strings.TrimSpace(b.Title + " " + b.Genre + " " + clip(b.Description, 60))
					if h, err := a.LLMFor(FeatScript, "", j.StudioID); err == nil {
						if pp, err := a.CoverPrompt(ctx, h, b); err == nil && pp != "" {
							prompt = pp
						}
					}
				}
			}
			brief := prompt
			switch {
			case isCover && real:
				noTitle, _ := j.Payload.V["noTitle"].(bool)
				brief = coverImagePrompt(b, prompt, !noTitle)
			case chID != "" && real:
				brief = avatarImagePrompt(prompt)
			}
			what := map[bool]string{true: "封面", false: "图片"}[isCover]
			if chID != "" {
				what = "头像"
			}
			jc.Progress(0.2, fmt.Sprintf("正在绘制%s（%s），通常需要 10–60 秒", what, map[bool]string{true: acc.Name, false: "内置生成器"}[real]))
			start := time.Now()
			files, err := p.GenerateImage(ctx, gen.ImageRequest{Prompt: brief, Aspect: "1:1", Width: 1200, Height: 1200, Count: count, Seed: j.ID, User: endUserID(j.CreatedBy)})
			a.noteCall(acc.ID, time.Since(start), err)
			if err != nil {
				return jobs.Permanent(withDetail("图片生成失败", fmt.Errorf("账号「%s」：%w", acc.Name, err)))
			}
			for i := range files {
				name, tags := fmt.Sprintf("%s %d", clip(prompt, 16), i+1), []string{"AI 生成"}
				switch {
				case chID != "":
					name, tags = "AI 头像", append(tags, "头像")
				case isCover:
					name, tags = fmt.Sprintf("《%s》AI 封面 %d", clip(b.Title, 20), i+1), append(tags, "封面")
				}
				meta := map[string]any{"prompt": brief}
				if isCover {
					meta["bookId"] = j.BookID // lets the book page offer every AI cover as a candidate
				}
				for k, v := range files[i].Meta {
					meta[k] = v
				}
				if err := save(&files[i], name, model.AssetImage, tags, meta); err != nil {
					return err
				}
			}
			if err := charge(len(files)); err != nil {
				return jobs.Permanent(err)
			}
			if chID != "" && len(assetIDs) > 0 {
				var as model.Asset
				if a.DB.First(&as, "id = ?", assetIDs[0]).Error == nil {
					a.DB.Model(&model.Character{}).Where("id = ?", chID).Update("avatar_url", a.Store.SignedPath(as.StorageKey, 10*365*24*time.Hour, ""))
					a.emit(j.StudioID, eventBook(j.BookID, "characters"))
				}
			}
			if set, _ := j.Payload.V["setCover"].(bool); set && isCover && len(assetIDs) > 0 {
				a.DB.Model(&model.Book{}).Where("id = ?", j.BookID).Update("cover_asset_id", assetIDs[0])
				a.emit(j.StudioID, eventBook(j.BookID, "book"))
			}
			if len(files) < count {
				jc.SetResult(map[string]any{"assetIds": assetIDs, "prompt": prompt})
				jc.Progress(1, fmt.Sprintf("已生成 %d 张，另有 %d 张没有生成成功（未扣积分）", len(files), count-len(files)))
				return nil
			}
		}
		jc.SetResult(map[string]any{"assetIds": assetIDs, "prompt": prompt})
		jc.Progress(1, fmt.Sprintf("已生成 %d 个结果", len(assetIDs)))
		return nil
	}
}

// EnqueueCharacterAvatar generates a portrait for a character with the image provider.
func (a *App) EnqueueCharacterAvatar(x *Access, c *model.Character, prompt string) (*model.Job, error) {
	if _, _, err := a.genAccount(model.KindImage, x.StudioID); err != nil {
		return nil, err
	}
	j := &model.Job{Type: "image_generate", StudioID: x.StudioID, BookID: c.BookID, RefType: "character", RefID: c.ID, Title: "AI 头像 · " + c.Name, CreatedBy: x.User.ID, MaxAttempts: 1,
		Payload: model.Map{V: map[string]any{"prompt": prompt, "count": 1, "characterId": c.ID}}}
	return j, a.Q.Enqueue(j)
}

func intFrom(v any) int {
	switch x := v.(type) {
	case float64:
		return int(x)
	case int:
		return x
	case int64:
		return int(x)
	}
	return 0
}

// CoverCandidate is one AI-generated cover the book page offers to pick from.
type CoverCandidate struct {
	ID        string    `json:"id"`
	URL       string    `json:"url"`
	Model     string    `json:"model"`
	CreatedAt time.Time `json:"createdAt"`
	Current   bool      `json:"current"`
}

// CoverOptions lists a book's AI covers (newest first) and what the image engine can do.
func (a *App) CoverOptions(b *model.Book) map[string]any {
	var list []model.Asset
	a.DB.Where("studio_id = ? AND kind = ? AND "+dbx.JSONText(a.DB, "meta", "bookId")+" = ?", b.StudioID, model.AssetImage, b.ID).
		Order("created_at desc").Limit(12).Find(&list)
	items := []CoverCandidate{}
	for i := range list {
		m, _ := list[i].Meta.V["model"].(string)
		items = append(items, CoverCandidate{ID: list[i].ID, URL: a.AssetURL(&list[i]), Model: m, CreatedAt: list[i].CreatedAt, Current: list[i].ID == b.CoverAssetID})
	}
	engine := map[string]any{"available": false, "lettering": false, "name": ""}
	if acc, _, err := a.genAccount(model.KindImage, b.StudioID); err == nil {
		engine = map[string]any{"available": true, "lettering": acc.Provider != "placeholder", "name": acc.Name}
	}
	return map[string]any{"items": items, "engine": engine, "price": a.Estimate(FeatImage, "", 1)}
}
