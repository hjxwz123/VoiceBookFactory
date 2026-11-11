package core

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"voicebook/internal/ai/tts"
	"voicebook/internal/jobs"
	"voicebook/internal/model"
)

// Official preview pack of the ~600 Qwen-Audio-TTS 3.0 base voices (shared by plus and flash).
const QwenBasePreviewPackURL = "https://help-static-aliyun-doc.aliyuncs.com/file-manage-files/zh-CN/20260720/tuuuqo/%E5%9F%BA%E7%A1%80%E9%9F%B3%E8%89%B2%E8%AF%95%E5%90%AC%E9%9F%B3%E9%A2%91%E5%8C%85.zip"

// providerAliases lets voices of one adapter run on accounts registered under an older name.
var providerAliases = map[string][]string{"qwen": {"qwen", "aliyun"}, "aliyun": {"aliyun", "qwen"}}

func providerNames(p string) []string {
	if a, ok := providerAliases[p]; ok {
		return a
	}
	return []string{p}
}

var activeTTS struct {
	sync.Mutex
	at   time.Time
	list []string
}

// ActiveTTSProviders returns adapters that have an enabled platform account. Built-in voices of other
// platforms are hidden so users never pick a voice nobody can synthesize.
func (a *App) ActiveTTSProviders() []string {
	activeTTS.Lock()
	defer activeTTS.Unlock()
	if time.Since(activeTTS.at) < 15*time.Second && activeTTS.list != nil {
		return activeTTS.list
	}
	var names []string
	a.DB.Model(&model.ProviderAccount{}).Where("kind = ? AND scope = ? AND enabled = ?", model.KindTTS, "platform", true).Distinct().Pluck("provider", &names)
	seen := map[string]bool{}
	out := []string{}
	for _, n := range names {
		for _, alias := range providerNames(n) {
			if !seen[alias] {
				seen[alias] = true
				out = append(out, alias)
			}
		}
	}
	if len(out) == 0 {
		out = []string{"-"} // keeps SQL "IN ?" valid
	}
	activeTTS.list, activeTTS.at = out, time.Now()
	return out
}

// InvalidateTTSProviders drops the cached provider list after accounts change, on every instance.
func (a *App) InvalidateTTSProviders() {
	a.dropTTSProviders()
	a.publish(topicCache, cacheMsg{Kind: "tts-providers"})
}

func (a *App) dropTTSProviders() {
	activeTTS.Lock()
	activeTTS.at = time.Time{}
	activeTTS.Unlock()
}

// UpsertCatalogVoices imports platform voices into the official library. Existing voices keep any edits made
// by operators; only missing entries are created (and missing preview links filled in).
func (a *App) UpsertCatalogVoices(provider, accountID string, list []tts.RemoteVoice) (int, error) {
	type key struct{ model, id string }
	var existing []model.Voice
	a.DB.Select("id", "model", "provider_voice_id", "sample_url", "sample_asset_id").Where("scope = ? AND provider IN ?", model.ScopeSystem, providerNames(provider)).Find(&existing)
	have := map[key]model.Voice{}
	for _, v := range existing {
		have[key{v.Model, v.ProviderVoiceID}] = v
	}
	var batch []model.Voice
	for i, rv := range list {
		if old, ok := have[key{rv.Model, rv.ProviderVoiceID}]; ok {
			if old.SampleURL == "" && rv.PreviewURL != "" {
				a.DB.Model(&model.Voice{}).Where("id = ?", old.ID).Update("sample_url", rv.PreviewURL)
			}
			continue
		}
		caps := catalogCaps(provider, rv.Model, rv.ProviderVoiceID)
		emotions := []string{}
		if caps.Emotion {
			emotions = caps.Emotions
		}
		order := 100 + i
		if rv.Base {
			order = 2000 + i
		}
		tier := firstNonEmpty(rv.PriceTier, model.TierStandard)
		lang := firstNonEmpty(rv.Language, "zh-CN")
		sample := sampleTextFor(lang)
		v := model.Voice{Scope: model.ScopeSystem, Name: rv.Name, Origin: model.VoicePreset, Provider: provider, ProviderAccountID: accountID, ProviderVoiceID: rv.ProviderVoiceID,
			Model: rv.Model, Language: lang, Gender: firstNonEmpty(rv.Gender, "unknown"), AgeGroup: firstNonEmpty(rv.AgeGroup, "young"), Tags: model.Strings(rv.Tags...),
			Description: rv.Description, PriceTier: tier, Status: model.VoiceReady, Listed: true, Recommended: rv.Recommended, CrowdPool: rv.CrowdPool,
			SortOrder: order, SampleURL: rv.PreviewURL, SampleText: sample, Emotions: model.Strings(emotions...)}
		v.Defaults.V = model.VoiceDefaults{Speed: 1}
		batch = append(batch, v)
	}
	if len(batch) == 0 {
		return 0, nil
	}
	if err := a.DB.CreateInBatches(&batch, 200).Error; err != nil {
		return 0, err
	}
	return len(batch), nil
}

// catalogCaps describes a catalog voice without account credentials.
func catalogCaps(provider, modelName, voiceID string) tts.Capabilities {
	if p, err := tts.New(tts.Account{Provider: provider}); err == nil {
		return tts.CapsFor(p, modelName, voiceID)
	}
	return tts.Capabilities{}
}

// sampleTextFor returns a short self-introduction in the voice's language (used for previews).
func sampleTextFor(lang string) string {
	l := strings.ToLower(lang)
	if l == "zh-hk" || l == "yue" {
		return "你好，好高兴识到你。呢把就系我嘅声，希望可以为你嘅故事添啲色彩。"
	}
	if t, ok := sampleTexts[strings.SplitN(l, "-", 2)[0]]; ok {
		return t
	}
	return "Hello, nice to meet you. This is my voice."
}

var sampleTexts = map[string]string{
	"zh": "你好，很高兴认识你。这是我的声音，希望能为你的故事增添色彩。",
	"en": "Hello there. Let me tell you a story that begins on a quiet winter night.",
	"ja": "こんにちは、はじめまして。これが私の声です。あなたの物語に彩りを添えられたら嬉しいです。",
	"ko": "안녕하세요, 만나서 반가워요. 이것이 제 목소리예요. 당신의 이야기에 색을 더해 드릴게요.",
	"es": "Hola, encantado de conocerte. Esta es mi voz; espero darle color a tu historia.",
	"pt": "Olá, prazer em conhecer você. Esta é a minha voz; espero dar vida à sua história.",
	"fr": "Bonjour, ravi de vous rencontrer. Voici ma voix ; j'espère donner vie à votre histoire.",
	"de": "Hallo, schön, dich kennenzulernen. Das ist meine Stimme – ich erzähle gern deine Geschichte.",
	"ru": "Здравствуйте, рад знакомству. Это мой голос, и я с радостью расскажу вашу историю.",
	"it": "Ciao, piacere di conoscerti. Questa è la mia voce: spero di dare colore alla tua storia.",
	"id": "Halo, senang bertemu denganmu. Inilah suaraku, semoga bisa menghidupkan ceritamu.",
	"ar": "مرحبًا، سعيد بلقائك. هذا هو صوتي، وأتمنى أن أضيف لقصتك لمسة جميلة.",
	"tr": "Merhaba, tanıştığıma memnun oldum. Bu benim sesim; hikâyene renk katmayı umuyorum.",
	"uk": "Привіт, радий знайомству. Це мій голос, і я із задоволенням розповім вашу історію.",
	"nl": "Hallo, leuk je te ontmoeten. Dit is mijn stem; ik vertel graag jouw verhaal.",
	"vi": "Xin chào, rất vui được gặp bạn. Đây là giọng nói của tôi, mong sẽ tô điểm cho câu chuyện của bạn.",
	"th": "สวัสดีค่ะ ยินดีที่ได้รู้จัก นี่คือเสียงของฉัน หวังว่าจะช่วยเติมสีสันให้เรื่องราวของคุณ",
	"pl": "Cześć, miło cię poznać. To mój głos – chętnie opowiem twoją historię.",
	"ro": "Bună, încântat de cunoștință. Aceasta este vocea mea și îți voi spune povestea cu drag.",
	"el": "Γεια σας, χαίρομαι που σας γνωρίζω. Αυτή είναι η φωνή μου και θα πω την ιστορία σας με χαρά.",
	"cs": "Dobrý den, rád vás poznávám. Tohle je můj hlas a rád vám povím váš příběh.",
	"fi": "Hei, hauska tavata. Tämä on minun ääneni, ja kerron mielelläni tarinasi.",
	"hi": "नमस्ते, आपसे मिलकर खुशी हुई। यह मेरी आवाज़ है, उम्मीद है यह आपकी कहानी में रंग भर देगी।",
}

// seedMiniMaxCatalog keeps the MiniMax system voices in the library (hidden until a MiniMax account is enabled).
// Each voice is listed once per catalog model: HD (premium) and Turbo (standard).
func (a *App) seedMiniMaxCatalog() {
	var list []tts.RemoteVoice
	for _, md := range tts.MiniMaxCatalogModels {
		for _, v := range tts.MiniMaxCatalog() {
			list = append(list, v.Remote(md))
		}
	}
	if n, err := a.UpsertCatalogVoices("minimax", "", list); err != nil {
		slog.Warn("seed minimax voices", "err", err)
	} else if n > 0 {
		slog.Info("seeded minimax voices", "count", n)
	}
}

// seedQwenCatalog keeps the Qianwen built-in voices in the library (hidden until a Qianwen account is enabled).
func (a *App) seedQwenCatalog() {
	var list []tts.RemoteVoice
	for _, v := range tts.QwenCatalog() {
		list = append(list, v.Remote())
	}
	if n, err := a.UpsertCatalogVoices("qwen", "", list); err != nil {
		slog.Warn("seed qwen voices", "err", err)
	} else if n > 0 {
		slog.Info("seeded qwen voices", "count", n)
	}
}

// StartPreviewPackImport downloads the official base-voice preview pack and attaches previews to voices.
func (a *App) StartPreviewPackImport(userID, url string) (*model.Job, error) {
	url = firstNonEmpty(strings.TrimSpace(url), QwenBasePreviewPackURL)
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return nil, Bad("请填写有效的下载地址")
	}
	j := &model.Job{Type: "voice_preview_import", Title: "导入基础音色试听音频", CreatedBy: userID, MaxAttempts: 2, Payload: model.Map{V: map[string]any{"url": url}}}
	return j, a.Q.Enqueue(j)
}

func (a *App) jobPreviewPackImport(ctx context.Context, jc *jobs.Context) error {
	url, _ := jc.Job.Payload.V["url"].(string)
	url = firstNonEmpty(url, QwenBasePreviewPackURL)
	dir, err := a.Store.TempDir("previews")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	zipPath := filepath.Join(dir, "pack.zip")
	jc.Progress(0.02, "正在下载官方试听音频包")
	if err := downloadFile(ctx, url, zipPath, func(done, total int64) {
		if total > 0 {
			jc.Progress(0.02+0.48*float64(done)/float64(total), fmt.Sprintf("正在下载试听音频包 %d/%d MB", done>>20, total>>20))
		}
	}); err != nil {
		return fmt.Errorf("下载失败：%w", err)
	}
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return jobs.Permanent(fmt.Errorf("音频包无法解压：%w", err))
	}
	defer zr.Close()
	files := map[string]*zip.File{}
	for _, f := range zr.File {
		if !f.FileInfo().IsDir() {
			files[strings.ToLower(path.Base(f.Name))] = f
		}
	}
	// which voices need which preview file
	var voices []model.Voice
	a.DB.Select("id", "model", "provider_voice_id", "name").Where("scope = ? AND provider IN ? AND sample_asset_id = ''", model.ScopeSystem, providerNames("qwen")).Find(&voices)
	want := map[string][]string{}
	for _, v := range voices {
		if cv := tts.QwenLookup(v.Model, v.ProviderVoiceID); cv != nil && cv.PreviewFile != "" {
			fn := strings.ToLower(cv.PreviewFile)
			want[fn] = append(want[fn], v.ID)
		}
	}
	if len(want) == 0 {
		jc.Progress(1, "所有基础音色都已有试听音频")
		return nil
	}
	done, missing := 0, 0
	for fn, ids := range want {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		zf, ok := files[fn]
		if !ok {
			missing++
			continue
		}
		rc, err := zf.Open()
		if err != nil {
			missing++
			continue
		}
		data, err := io.ReadAll(io.LimitReader(rc, 20<<20))
		rc.Close()
		if err != nil || len(data) == 0 {
			missing++
			continue
		}
		ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(fn)), ".")
		as, err := a.SaveAssetBytes(ctx, AssetInput{Scope: model.ScopeSystem, Kind: model.AssetPreview, Name: strings.TrimSuffix(zf.Name, filepath.Ext(zf.Name)) + " 试听", Ext: firstNonEmpty(ext, "wav"), Origin: "system", NoPeaks: true}, data)
		if err != nil {
			return err
		}
		a.DB.Model(&model.Voice{}).Where("id IN ?", ids).Update("sample_asset_id", as.ID)
		done++
		if done%20 == 0 {
			jc.Progress(0.5+0.5*float64(done)/float64(len(want)), fmt.Sprintf("已导入 %d/%d 个试听音频", done, len(want)))
		}
	}
	jc.Progress(1, fmt.Sprintf("已导入 %d 个试听音频%s", done, map[bool]string{true: fmt.Sprintf("，%d 个在音频包中未找到", missing), false: ""}[missing > 0]))
	jc.SetResult(map[string]any{"imported": done, "missing": missing})
	return nil
}

func downloadFile(ctx context.Context, url, dst string, progress func(done, total int64)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := (&http.Client{Timeout: 30 * time.Minute}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer f.Close()
	buf := make([]byte, 256<<10)
	var done int64
	last := time.Now()
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, err := f.Write(buf[:n]); err != nil {
				return err
			}
			done += int64(n)
			if time.Since(last) > time.Second {
				progress(done, resp.ContentLength)
				last = time.Now()
			}
		}
		if rerr == io.EOF {
			return nil
		}
		if rerr != nil {
			return rerr
		}
	}
}
