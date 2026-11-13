package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"time"

	"voicebook/internal/ai/gen"
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
	DurationMs int    `json:"durationMs"`
	Count      int    `json:"count"`
	BookID     string `json:"bookId"`
	SetCover   bool   `json:"setCover"`
	NoTitle    bool   `json:"noTitle"` // book covers: keep the title off the picture
	// music from providers that make songs (Suno): sung rather than instrumental, own lyrics, style, title
	Vocal  bool   `json:"vocal"`
	Lyrics string `json:"lyrics"`
	Style  string `json:"style"`
	Title  string `json:"title"`
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
		Payload: model.Map{V: map[string]any{"prompt": prompt, "durationMs": in.DurationMs, "count": count, "setCover": in.SetCover, "noTitle": in.NoTitle,
			"vocal": in.Vocal, "lyrics": strings.TrimSpace(in.Lyrics), "style": strings.TrimSpace(in.Style), "title": strings.TrimSpace(in.Title)}}}
	return j, a.Q.Enqueue(j)
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
		save := func(f *gen.File, name, assetKind string, tags []string, meta map[string]any) error {
			license := "commercial"
			if meta["provider"] == "suno" { // a third-party service: its own terms decide whether the song may be used commercially
				license = "provider_terms"
			}
			as, err := a.SaveAssetBytes(ctx, AssetInput{StudioID: j.StudioID, OwnerUserID: j.CreatedBy, Kind: assetKind, Name: name, Ext: f.Ext, Mime: f.Mime, Origin: "ai", License: license, Tags: tags, CreatedBy: j.CreatedBy, Description: prompt, Meta: meta}, f.Data)
			if err != nil {
				return err
			}
			assetIDs = append(assetIDs, as.ID)
			return nil
		}
		charge := func(n int) error {
			_, err := a.ChargeCredits(Charge{StudioID: j.StudioID, UserID: j.CreatedBy, BookID: j.BookID, JobID: j.ID, Feature: feature, Quantity: float64(n), Unit: "calls", Provider: acc.Provider, AccountID: acc.ID, BYOK: byok})
			return err
		}
		switch kind {
		case model.KindSFX:
			p, err := gen.NewSFX(a.genAcc(acc))
			if err != nil {
				return jobs.Permanent(err)
			}
			if dur <= 0 {
				dur = 4000
			}
			for i := 0; i < count; i++ {
				jc.Progress(float64(i)/float64(count), fmt.Sprintf("正在生成第 %d/%d 个候选", i+1, count))
				f, err := p.GenerateSFX(ctx, gen.SFXRequest{Prompt: prompt, DurationMs: dur + i*700, Seed: fmt.Sprint(j.ID, i)})
				if err != nil {
					return jobs.Permanent(withDetail("音效生成失败", err))
				}
				label := sfxKindNames[fmt.Sprint(f.Meta["kind"])]
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
