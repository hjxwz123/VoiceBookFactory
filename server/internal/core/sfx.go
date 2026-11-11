package core

import (
	"archive/zip"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"log/slog"
	"math"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"voicebook/internal/audio"
	dbx "voicebook/internal/db"
	"voicebook/internal/jobs"
	"voicebook/internal/model"
)

// ---------------------------------------------------------------------------
// Official sound-effect library.
//
// sfx_catalog.json is produced by scripts/build_sfx_catalog.py from recordings that are, per file, CC0
// or public domain (PDsounds.org and CC0 uploads on Wikimedia Commons, CC0 packs on OpenGameArt). It
// lists the clip to keep from each source; administrators import the library with one click (job
// "sfx_import"): each sound is downloaded, cut, faded, loudness-normalised and stored as an official
// asset. Footsteps are assembled from single-step recordings. Re-running fills gaps.
// ---------------------------------------------------------------------------

//go:embed sfx_catalog.json
var sfxCatalogJSON []byte

type SfxSteps struct {
	Members    []string `json:"members"`
	IntervalMs int      `json:"intervalMs"`
	Count      int      `json:"count"`
}

type SfxSound struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Kind         string    `json:"kind"` // sfx | ambience
	Category     string    `json:"category"`
	CategoryName string    `json:"categoryName"`
	Tags         []string  `json:"tags"`
	URL          string    `json:"url"`
	Member       string    `json:"member,omitempty"`
	Steps        *SfxSteps `json:"steps,omitempty"`
	// clip hints (ms): an explicit window, the loudest stretch of a given length, a length cap; the
	// import works out the final window from the audio (silence trimmed for one-shot effects)
	Start      *int64 `json:"start,omitempty"`
	Dur        *int64 `json:"dur,omitempty"`
	PickMs     int64  `json:"pickMs,omitempty"`
	CapMs      int64  `json:"capMs,omitempty"`
	DurationMs int64  `json:"durationMs"` // expected length (approximate, for display)
	SourceMs   int64  `json:"sourceMs"`
	Page       string `json:"page"`
	License    string `json:"license"`
	LicenseURL string `json:"licenseUrl"`
	Source     string `json:"source"`
	Author     string `json:"author"`
}

type SfxCatalogData struct {
	Version     int             `json:"version"`
	GeneratedAt string          `json:"generatedAt"`
	Categories  []MusicCategory `json:"categories"`
	Sounds      []SfxSound      `json:"sounds"`
}

var (
	sfxOnce sync.Once
	sfxData SfxCatalogData
)

// SfxCatalog returns the embedded sound-effect catalog.
func SfxCatalog() *SfxCatalogData {
	sfxOnce.Do(func() {
		if err := json.Unmarshal(sfxCatalogJSON, &sfxData); err != nil {
			slog.Error("sfx catalog", "err", err)
		}
	})
	return &sfxData
}

// importedSfx maps catalog IDs to asset IDs of sounds already in the library.
func (a *App) importedSfx() map[string]string {
	var assets []model.Asset
	a.DB.Select("id", "meta").Where("scope = ? AND kind IN ? AND origin = ?", model.ScopeSystem, []string{model.AssetSFX, model.AssetAmbience}, "library").Find(&assets)
	out := map[string]string{}
	for _, as := range assets {
		if id, ok := as.Meta.V["libraryId"].(string); ok && id != "" {
			out[id] = as.ID
		}
	}
	return out
}

// SfxCategoryStatus counts catalog and imported sounds of one category.
type SfxCategoryStatus struct {
	Key       string `json:"key"`
	Name      string `json:"name"`
	Total     int    `json:"total"`
	Imported  int    `json:"imported"`
	Effects   int    `json:"effects"`   // imported one-shot effects
	Ambiences int    `json:"ambiences"` // imported ambience beds
}

type SfxStatus struct {
	Total       int                 `json:"total"`
	Imported    int                 `json:"imported"`
	Effects     int                 `json:"effects"`
	Ambiences   int                 `json:"ambiences"`
	Categories  []SfxCategoryStatus `json:"categories"`
	GeneratedAt string              `json:"generatedAt"`
	Job         *model.Job          `json:"job"`
	ContactSet  bool                `json:"contactSet"`
}

// SfxLibraryStatus summarises the catalog against what has been imported.
func (a *App) SfxLibraryStatus() SfxStatus {
	cat := SfxCatalog()
	done := a.importedSfx()
	rows := map[string]*SfxCategoryStatus{}
	var order []string
	for _, c := range cat.Categories {
		rows[c.Key] = &SfxCategoryStatus{Key: c.Key, Name: c.Name}
		order = append(order, c.Key)
	}
	st := SfxStatus{Total: len(cat.Sounds), GeneratedAt: cat.GeneratedAt, ContactSet: a.Cfg.BotContact != ""}
	for _, s := range cat.Sounds {
		if s.Kind == model.AssetAmbience {
			st.Ambiences++
		} else {
			st.Effects++
		}
		r := rows[s.Category]
		if r == nil {
			continue
		}
		r.Total++
		if done[s.ID] != "" {
			r.Imported++
			st.Imported++
			if s.Kind == model.AssetAmbience {
				r.Ambiences++
			} else {
				r.Effects++
			}
		}
	}
	for _, k := range order {
		st.Categories = append(st.Categories, *rows[k])
	}
	var job model.Job
	if a.DB.Where("type = ?", "sfx_import").Order("created_at desc").First(&job).Error == nil {
		st.Job = &job
	}
	return st
}

// StartSfxImport queues the library import unless one is already running.
func (a *App) StartSfxImport(operatorID string) (*model.Job, error) {
	var running model.Job
	if a.DB.Where("type = ? AND status IN ?", "sfx_import", []string{model.JobQueued, model.JobRunning}).First(&running).Error == nil {
		return &running, nil
	}
	if !a.FF.Available() {
		return nil, Bad("服务器没有安装 ffmpeg，无法处理音效")
	}
	j := &model.Job{Type: "sfx_import", Title: "导入官方音效库", MaxAttempts: 2, CreatedBy: operatorID}
	if err := a.Q.Enqueue(j); err != nil {
		return nil, err
	}
	return j, nil
}

func (a *App) jobSfxImport(ctx context.Context, jc *jobs.Context) error {
	cat := SfxCatalog()
	done := a.importedSfx()
	dir, err := os.MkdirTemp("", "sfx-import-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	dl := &sfxDownloads{ua: a.botUA(), client: &http.Client{Timeout: 4 * time.Minute}, dir: dir, files: map[string]string{}}
	var todo []SfxSound
	for _, s := range cat.Sounds {
		if done[s.ID] == "" {
			todo = append(todo, s)
		}
	}
	var imported, failed int
	var failures []string
	blocked := map[string]bool{} // hosts that refused us: no further requests this run
	for i, s := range todo {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		jc.Progress(float64(i)/float64(max(len(todo), 1)), fmt.Sprintf("正在导入第 %d/%d 个：%s · %s", i+1, len(todo), s.CategoryName, s.Name))
		if blocked[hostOf(s.URL)] {
			failed++
			continue
		}
		data, err := a.renderSfx(ctx, dl, s)
		if errors.Is(err, errRobotPolicy) {
			blocked[hostOf(s.URL)] = true
		}
		if err == nil {
			_, err = a.SaveAssetBytes(ctx, AssetInput{
				Scope: model.ScopeSystem, Kind: s.Kind, Name: s.Name, Ext: "mp3", Origin: "library", License: "commercial",
				Tags: s.Tags, Listed: true, Description: sfxDescription(s),
				Meta: map[string]any{"libraryId": s.ID, "category": s.Category, "categoryName": s.CategoryName, "artist": s.Author, "source": s.Source,
					"page": s.Page, "license": s.License, "licenseUrl": s.LicenseURL, "attribution": false},
			}, data)
		}
		if err != nil {
			failed++
			if len(failures) < 20 {
				failures = append(failures, s.Name+"："+err.Error())
			}
			slog.Warn("sfx import", "sound", s.Name, "err", err)
		} else {
			imported++
		}
	}
	if imported > 0 {
		a.retirePlaceholderSfx()
	}
	jc.SetResult(map[string]any{"imported": imported, "failed": failed, "failures": failures, "total": len(cat.Sounds)})
	msg := fmt.Sprintf("导入完成：新增 %d 个音效", imported)
	if failed > 0 {
		msg += fmt.Sprintf("，%d 个下载或处理失败（可稍后再次导入补全）", failed)
	}
	jc.Progress(1, msg)
	if len(blocked) > 0 {
		return jobs.Permanent(fmt.Errorf("新增 %d 个；%w", imported, errRobotPolicy))
	}
	if imported == 0 && failed > 0 {
		return jobs.Permanent(errors.New("全部失败，请检查服务器能否访问 upload.wikimedia.org 与 opengameart.org"))
	}
	return nil
}

// sfxDownloads fetches each source file once per import (several sounds come from the same pack).
type sfxDownloads struct {
	ua     string
	client *http.Client
	dir    string
	files  map[string]string
	n      int
}

func (d *sfxDownloads) get(ctx context.Context, url string) (string, error) {
	if p, ok := d.files[url]; ok {
		return p, nil
	}
	data, ext, err := downloadMusic(ctx, d.client, d.ua, MusicTrack{URL: url})
	if err != nil {
		return "", err
	}
	d.n++
	p := filepath.Join(d.dir, fmt.Sprintf("src%04d.%s", d.n, ext))
	if err := os.WriteFile(p, data, 0o644); err != nil {
		return "", err
	}
	d.files[url] = p
	return p, nil
}

func (d *sfxDownloads) member(zipPath, name string) (string, error) {
	key := zipPath + "#" + name
	if p, ok := d.files[key]; ok {
		return p, nil
	}
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return "", fmt.Errorf("无法打开压缩包：%w", err)
	}
	defer zr.Close()
	for _, f := range zr.File {
		if f.Name != name {
			continue
		}
		if f.UncompressedSize64 > 200<<20 {
			return "", errors.New("压缩包内文件过大")
		}
		rc, err := f.Open()
		if err != nil {
			return "", err
		}
		data, err := io.ReadAll(io.LimitReader(rc, 200<<20))
		rc.Close()
		if err != nil {
			return "", err
		}
		d.n++
		p := filepath.Join(d.dir, fmt.Sprintf("m%04d%s", d.n, strings.ToLower(path.Ext(name))))
		if err := os.WriteFile(p, data, 0o644); err != nil {
			return "", err
		}
		d.files[key] = p
		return p, nil
	}
	return "", fmt.Errorf("压缩包里没有 %s", name)
}

// Loudness of library sounds: effects sit a little under dialogue, ambience well below it (the
// ambience track is further lowered and ducked in the mix).
const (
	sfxTargetLUFS      = -20.0
	ambienceTargetLUFS = -26.0
	shortPeakDB        = -6.0 // clips too short for loudness measurement are peak-normalised
)

// renderSfx produces the finished MP3 of one catalog sound.
func (a *App) renderSfx(ctx context.Context, dl *sfxDownloads, s SfxSound) ([]byte, error) {
	src, err := dl.get(ctx, s.URL)
	if err != nil {
		return nil, err
	}
	in := src
	var start, dur int64
	switch {
	case s.Steps != nil:
		if in, err = a.composeSteps(ctx, dl, src, s); err != nil {
			return nil, err
		}
	case s.Member != "":
		if in, err = dl.member(src, s.Member); err != nil {
			return nil, err
		}
	}
	if s.Steps == nil {
		if start, dur, err = a.chooseClip(ctx, in, s); err != nil {
			return nil, err
		}
	}
	ch := 2
	if info, err := a.FF.Probe(ctx, in); err == nil && info.Channels == 1 {
		ch = 1
	}
	if dur <= 0 {
		if info, err := a.FF.Probe(ctx, in); err == nil {
			dur = info.DurationMs - start
		}
	}
	if dur <= 0 {
		return nil, errors.New("无法读取音频时长")
	}
	fadeIn, fadeOut := 0.006, math.Min(0.25, float64(dur)/1000*0.2)
	if s.Kind == model.AssetAmbience {
		fadeIn, fadeOut = math.Min(0.6, float64(dur)/1000*0.1), math.Min(1.2, float64(dur)/1000*0.15)
	}
	secs := float64(dur) / 1000
	cut := filepath.Join(dl.dir, "cut.wav")
	filter := fmt.Sprintf("afade=t=in:st=0:d=%.3f,afade=t=out:st=%.3f:d=%.3f", fadeIn, math.Max(0, secs-fadeOut), fadeOut)
	if err := a.FF.Cut(ctx, in, cut, start, dur, ch, filter); err != nil {
		return nil, err
	}
	out := filepath.Join(dl.dir, "out.mp3")
	opt := audio.EncodeOptions{Format: "mp3", Bitrate: 192, SampleRate: 44100, Channels: ch}
	if secs >= 3 {
		target := sfxTargetLUFS
		if s.Kind == model.AssetAmbience {
			target = ambienceTargetLUFS
		}
		if m, err := a.FF.MeasureLoudness(ctx, cut, target, -1.5); err == nil && m.InputI > -70 {
			opt.TargetI, opt.TargetTP, opt.Measured = target, -1.5, m
		}
	}
	if opt.TargetI == 0 {
		peak, err := a.FF.MaxVolume(ctx, cut)
		if err != nil {
			return nil, err
		}
		if peak < -60 {
			return nil, errors.New("音频几乎是静音")
		}
		opt.ExtraFilter = fmt.Sprintf("volume=%.2fdB", shortPeakDB-peak)
	}
	if err := a.FF.Encode(ctx, cut, out, opt); err != nil {
		return nil, err
	}
	return os.ReadFile(out)
}

// chooseClip works out which part of a source recording becomes the library sound: an explicit
// window, the loudest stretch of a long recording, or (ambience) a bed after the first second; one-shot
// effects then lose leading/trailing silence and keep a short tail.
func (a *App) chooseClip(ctx context.Context, path string, s SfxSound) (int64, int64, error) {
	const rate = 22050
	pcm, err := a.FF.DecodePCM(ctx, path, rate, 1, "")
	if err != nil {
		return 0, 0, err
	}
	const hop = rate / 50 // 20 ms frames
	n := len(pcm.Data) / hop
	if n == 0 {
		return 0, 0, errors.New("音频太短或无法解码")
	}
	db := make([]float64, n)
	peak := 0.0
	for i := 0; i < n; i++ {
		var e float64
		for _, v := range pcm.Data[i*hop : (i+1)*hop] {
			e += float64(v) * float64(v)
			peak = math.Max(peak, math.Abs(float64(v)))
		}
		db[i] = 10 * math.Log10(e/hop+1e-12)
	}
	if 20*math.Log10(peak+1e-9) < -45 {
		return 0, 0, errors.New("音量过低")
	}
	fs := 0.02
	total := float64(n) * fs
	capS := float64(s.CapMs) / 1000
	if capS <= 0 {
		capS = 12
		if s.Kind == model.AssetAmbience {
			capS = 45
		}
	}
	var start, dur float64
	switch {
	case s.Start != nil || s.Dur != nil:
		if s.Start != nil {
			start = math.Min(float64(*s.Start)/1000, math.Max(0, total-1))
		}
		dur = total - start
		if s.Dur != nil {
			dur = math.Min(float64(*s.Dur)/1000, dur)
		}
	case s.PickMs > 0:
		w := float64(s.PickMs) / 1000
		if total <= w {
			start, dur = 0, total
		} else {
			win := int(w / fs)
			var acc, best float64
			bi := 0
			for i := 0; i < n; i++ {
				acc += math.Pow(10, db[i]/10)
				if i >= win {
					acc -= math.Pow(10, db[i-win]/10)
				}
				if i >= win-1 && acc > best {
					best, bi = acc, i-win+1
				}
			}
			start, dur = float64(bi)*fs, w
		}
	case s.Kind == model.AssetAmbience:
		if total > 8 {
			start = math.Min(1, total*0.05)
			dur = math.Min(total-start-0.3, capS)
		} else {
			dur = total
		}
	default:
		dur = total
	}
	if s.Kind != model.AssetAmbience && total >= 1 { // sub-second sources (whooshes, hits) are kept whole
		lo, hi := int(start/fs), min(n, int((start+dur)/fs))
		loud := -120.0
		for i := lo; i < hi; i++ {
			loud = math.Max(loud, db[i])
		}
		thr := math.Max(loud-42, -62)
		on, off := lo, hi-1
		for on < hi && db[on] <= thr {
			on++
		}
		for off > on && db[off] <= thr {
			off--
		}
		s2 := math.Max(start, float64(on)*fs-0.03)
		e2 := math.Min(start+dur, float64(off+1)*fs+0.25)
		start, dur = s2, math.Min(e2-s2, capS)
	}
	if dur < 0.08 {
		return 0, 0, errors.New("截取后的音频太短")
	}
	return int64(start * 1000), int64(dur * 1000), nil
}

// composeSteps lays single footstep recordings out as a walking (or running) sequence, with slight,
// deterministic variation in timing and level so it does not sound mechanical.
func (a *App) composeSteps(ctx context.Context, dl *sfxDownloads, zipPath string, s SfxSound) (string, error) {
	st := s.Steps
	if len(st.Members) == 0 || st.Count <= 0 || st.IntervalMs <= 0 {
		return "", errors.New("脚步序列配置无效")
	}
	const rate = 44100
	var steps []*audio.PCM
	for _, m := range st.Members {
		p, err := dl.member(zipPath, m)
		if err != nil {
			return "", err
		}
		pcm, err := a.FF.DecodePCM(ctx, p, rate, 1, "")
		if err != nil {
			return "", err
		}
		pcm = audio.TrimSilence(pcm, -50, 5)
		if pcm.Frames() == 0 {
			return "", fmt.Errorf("脚步录音 %s 是静音", m)
		}
		steps = append(steps, pcm)
	}
	h := fnv.New32a()
	h.Write([]byte(s.ID))
	seed := h.Sum32()
	rnd := func() float64 { // xorshift, deterministic per sound
		seed ^= seed << 13
		seed ^= seed >> 17
		seed ^= seed << 5
		return float64(seed%10000) / 10000
	}
	longest := 0
	for _, p := range steps {
		longest = max(longest, p.Frames())
	}
	total := (st.Count-1)*st.IntervalMs*rate/1000 + longest + rate/5
	mix := make([]float32, total)
	for i := 0; i < st.Count; i++ {
		p := steps[(i+int(rnd()*float64(len(steps))))%len(steps)]
		at := (i*st.IntervalMs + int((rnd()-0.5)*0.12*float64(st.IntervalMs))) * rate / 1000
		gain := float32(0.82 + 0.18*rnd())
		for k, v := range p.Data {
			if j := at + k; j >= 0 && j < total {
				mix[j] += v * gain
			}
		}
	}
	out := filepath.Join(dl.dir, "steps.wav")
	if err := audio.WriteWAVFile(out, &audio.PCM{Rate: rate, Channels: 1, Data: mix}); err != nil {
		return "", err
	}
	return out, nil
}

func sfxDescription(s SfxSound) string {
	lic := "CC0 公共领域贡献，可免费商用，无需署名"
	if s.License != "CC0" {
		lic = "公有领域，可免费商用，无需署名"
	}
	who := s.Author
	if who == "" {
		who = "佚名"
	}
	return truncateStr(fmt.Sprintf("%s · %s · %s（来源：%s）", s.CategoryName, who, lic, s.Source), 490)
}

// retirePlaceholderSfx hides the synthesized demo effects once real recordings are in the library.
// Lines that already use them keep working.
func (a *App) retirePlaceholderSfx() {
	var n int64
	a.DB.Model(&model.Asset{}).Where("scope = ? AND kind IN ? AND origin = ? AND listed = ?", model.ScopeSystem, []string{model.AssetSFX, model.AssetAmbience}, "library", true).Count(&n)
	if n < 60 {
		return
	}
	a.DB.Model(&model.Asset{}).Where("scope = ? AND kind IN ? AND origin = ?", model.ScopeSystem, []string{model.AssetSFX, model.AssetAmbience}, "system").Update("listed", false)
}

// SfxCategories lists library categories that have imported sounds of a kind (for filter chips).
func (a *App) SfxCategories(kind string) []map[string]any {
	out := []map[string]any{}
	for _, c := range a.SfxLibraryStatus().Categories {
		n := c.Imported
		switch kind {
		case model.AssetSFX:
			n = c.Effects
		case model.AssetAmbience:
			n = c.Ambiences
		}
		if n > 0 {
			out = append(out, map[string]any{"key": c.Key, "name": c.Name, "count": n})
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Matching a script's sound cue ("（远处传来狗叫声）") against hundreds of library sounds: rank by how
// much of the cue the name and tags cover, then let the model choose among the best few dozen.
// ---------------------------------------------------------------------------

func sfxScore(cue string, as model.Asset) int {
	cue = strings.TrimSpace(cue)
	if cue == "" {
		return 0
	}
	score := 0
	words := append([]string{as.Name}, as.Tags.V...)
	for i, w := range words {
		w = strings.TrimSpace(w)
		if utf8.RuneCountInString(w) < 1 {
			continue
		}
		weight := 1
		if i == 0 {
			weight = 2 // the name says what the sound is; tags only hint
		}
		if strings.Contains(cue, w) && utf8.RuneCountInString(w) >= 2 {
			score += 6 * weight * utf8.RuneCountInString(w)
			continue
		}
		r := []rune(w)
		for k := 0; k+1 < len(r); k++ { // shared two-character words ("敲门" in "急促的敲门声")
			if strings.Contains(cue, string(r[k:k+2])) {
				score += 3 * weight
			}
		}
		if len(r) == 1 && strings.ContainsRune(cue, r[0]) {
			score += weight
		}
	}
	return score
}

// rankSfx returns the n best-matching assets for a cue (only those that match at all).
func rankSfx(cue string, assets []model.Asset, n int) []model.Asset {
	type scored struct {
		a model.Asset
		s int
	}
	var list []scored
	for _, as := range assets {
		if s := sfxScore(cue, as); s > 0 {
			list = append(list, scored{as, s})
		}
	}
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].s != list[j].s {
			return list[i].s > list[j].s
		}
		return list[i].a.Origin == "library" && list[j].a.Origin != "library" // real recordings first
	})
	out := make([]model.Asset, 0, min(n, len(list)))
	for i := 0; i < len(list) && i < n; i++ {
		out = append(out, list[i].a)
	}
	return out
}

var (
	libraryOrderOnce sync.Once
	libraryOrderSQL  string
)

// LibraryOrder is the ORDER BY for asset lists: the studio's own files first (newest first), then the
// official library in catalog order (经典名曲 first, then by style or scene) and by name.
func (a *App) LibraryOrder() string {
	libraryOrderOnce.Do(func() {
		var b strings.Builder
		b.WriteString("CASE WHEN scope = 'system' THEN 1 ELSE 0 END, CASE WHEN scope = 'system' THEN CASE " + dbx.JSONText(a.DB, "meta", "category"))
		i := 0
		for _, c := range MusicCatalog().Categories {
			fmt.Fprintf(&b, " WHEN '%s' THEN %d", strings.ReplaceAll(c.Key, "'", ""), i)
			i++
		}
		for _, c := range SfxCatalog().Categories {
			fmt.Fprintf(&b, " WHEN '%s' THEN %d", strings.ReplaceAll(c.Key, "'", ""), i)
			i++
		}
		fmt.Fprintf(&b, " ELSE %d END END, CASE WHEN scope = 'system' THEN name END, created_at desc", i)
		libraryOrderSQL = b.String()
	})
	return libraryOrderSQL
}
