package core

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"log/slog"
	"net/http"
	neturl "net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	dbx "voicebook/internal/db"
	"voicebook/internal/jobs"
	"voicebook/internal/model"
)

// ---------------------------------------------------------------------------
// Official background-music library.
//
// music_catalog.json is produced by scripts/build_music_catalog.py from sources whose files are, per
// file, CC0 or public domain (Free Music Archive and Musopen recordings on Wikimedia Commons, a few
// OpenGameArt pieces). It lists metadata and download URLs only; administrators import the audio
// into the platform library with one click (job "music_import"), and can re-run it to fill gaps.
// ---------------------------------------------------------------------------

//go:embed music_catalog.json
var musicCatalogJSON []byte

type MusicTrack struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	Artist       string   `json:"artist"`
	Category     string   `json:"category"`
	CategoryName string   `json:"categoryName"`
	Tags         []string `json:"tags"`
	DurationMs   int64    `json:"durationMs"`
	URL          string   `json:"url"`
	Original     string   `json:"original"`
	Page         string   `json:"page"`
	License      string   `json:"license"`
	LicenseURL   string   `json:"licenseUrl"`
	Source       string   `json:"source"`
}

type MusicCategory struct {
	Key  string   `json:"key"`
	Name string   `json:"name"`
	Tags []string `json:"tags"`
}

type MusicCatalogData struct {
	Version     int             `json:"version"`
	GeneratedAt string          `json:"generatedAt"`
	Categories  []MusicCategory `json:"categories"`
	Tracks      []MusicTrack    `json:"tracks"`
}

var (
	musicOnce sync.Once
	musicData MusicCatalogData
)

// MusicCatalog returns the embedded catalog.
func MusicCatalog() *MusicCatalogData {
	musicOnce.Do(func() {
		if err := json.Unmarshal(musicCatalogJSON, &musicData); err != nil {
			slog.Error("music catalog", "err", err)
		}
	})
	return &musicData
}

// botUA identifies library downloads the way Wikimedia's User-Agent policy asks: client name and
// version plus contact information (the site address and the operator contact, if configured).
func (a *App) botUA() string {
	contact := a.Cfg.PublicURL
	if a.Cfg.BotContact != "" {
		contact += "; " + a.Cfg.BotContact
	}
	return "VoicebookAssetBot/1.0 (+" + contact + ") Go-http-client/1.1"
}

// errRobotPolicy means a source refused us for crawling too fast or without proper identification.
// Imports send no further requests to that host (the rest of the library still imports) and tell the
// operator what to change.
var errRobotPolicy = errors.New("来源网站拒绝了下载（要求遵守其机器人访问规范）：请在服务器环境变量 VB_BOT_CONTACT 中填写运营联系邮箱或网址，过一段时间再点「补全导入」；已导入的不受影响")

// hostOf returns the host of a download URL ("" if it cannot be parsed).
func hostOf(u string) string {
	if pu, err := neturl.Parse(u); err == nil {
		return pu.Host
	}
	return ""
}

// libraryPace spaces out requests to one host (Wikimedia asks bots to keep load low).
type libraryPace struct {
	mu   sync.Mutex
	last map[string]time.Time
}

func (p *libraryPace) wait(ctx context.Context, host string, gap time.Duration) error {
	p.mu.Lock()
	if p.last == nil {
		p.last = map[string]time.Time{}
	}
	d := time.Until(p.last[host].Add(gap))
	p.last[host] = time.Now().Add(max(d, 0))
	p.mu.Unlock()
	if d <= 0 {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

var libPace libraryPace

// importedMusic maps catalog IDs to asset IDs of tracks already in the library.
func (a *App) importedMusic() map[string]string {
	var assets []model.Asset
	a.DB.Select("id", "meta").Where("scope = ? AND kind = ? AND origin = ?", model.ScopeSystem, model.AssetBGM, "library").Find(&assets)
	out := map[string]string{}
	for _, as := range assets {
		if id, ok := as.Meta.V["libraryId"].(string); ok && id != "" {
			out[id] = as.ID
		}
	}
	return out
}

// MusicCategoryStatus counts catalog and imported tracks of one category.
type MusicCategoryStatus struct {
	Key      string `json:"key"`
	Name     string `json:"name"`
	Total    int    `json:"total"`
	Imported int    `json:"imported"`
}

type MusicStatus struct {
	Total       int                   `json:"total"`
	Imported    int                   `json:"imported"`
	Minutes     int64                 `json:"minutes"`
	Categories  []MusicCategoryStatus `json:"categories"`
	GeneratedAt string                `json:"generatedAt"`
	Job         *model.Job            `json:"job"`
	ContactSet  bool                  `json:"contactSet"` // VB_BOT_CONTACT configured (sources ask bots for contact details)
}

// MusicLibraryStatus summarises the catalog against what has been imported.
func (a *App) MusicLibraryStatus() MusicStatus {
	cat := MusicCatalog()
	done := a.importedMusic()
	type row = MusicCategoryStatus
	rows := map[string]*row{}
	var order []string
	for _, c := range cat.Categories {
		rows[c.Key] = &row{Key: c.Key, Name: c.Name}
		order = append(order, c.Key)
	}
	var minutes int64
	imported := 0 // only catalog tracks: retired ones (still in the database) do not count
	for _, t := range cat.Tracks {
		r := rows[t.Category]
		if r == nil {
			continue
		}
		r.Total++
		if done[t.ID] != "" {
			r.Imported++
			imported++
		}
		minutes += t.DurationMs
	}
	list := make([]row, 0, len(order))
	for _, k := range order {
		list = append(list, *rows[k])
	}
	st := MusicStatus{Total: len(cat.Tracks), Imported: imported, Minutes: minutes / 60000, Categories: list, GeneratedAt: cat.GeneratedAt, ContactSet: a.Cfg.BotContact != ""}
	var job model.Job
	if a.DB.Where("type = ?", "music_import").Order("created_at desc").First(&job).Error == nil {
		st.Job = &job
	}
	return st
}

// StartMusicImport queues the library import unless one is already running.
func (a *App) StartMusicImport(operatorID string) (*model.Job, error) {
	var running model.Job
	if a.DB.Where("type = ? AND status IN ?", "music_import", []string{model.JobQueued, model.JobRunning}).First(&running).Error == nil {
		return &running, nil
	}
	j := &model.Job{Type: "music_import", Title: "导入官方配乐库", MaxAttempts: 2}
	if err := a.Q.Enqueue(j); err != nil {
		return nil, err
	}
	return j, nil
}

func (a *App) jobMusicImport(ctx context.Context, jc *jobs.Context) error {
	cat := MusicCatalog()
	done := a.importedMusic()
	client := &http.Client{Timeout: 3 * time.Minute}
	var imported, failed int
	var failures []string
	blocked := map[string]bool{}
	todo := 0
	for _, t := range cat.Tracks {
		if done[t.ID] == "" {
			todo++
		}
	}
	n := 0
	for _, t := range cat.Tracks {
		if done[t.ID] != "" {
			continue
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		n++
		jc.Progress(float64(n-1)/float64(max(todo, 1)), fmt.Sprintf("正在导入第 %d/%d 首：%s", n, todo, t.Title))
		if blocked[hostOf(t.URL)] {
			failed++
			continue
		}
		data, ext, err := downloadMusic(ctx, client, a.botUA(), t)
		if errors.Is(err, errRobotPolicy) {
			blocked[hostOf(t.URL)] = true
		}
		if err == nil {
			_, err = a.SaveAssetBytes(ctx, AssetInput{
				Scope: model.ScopeSystem, Kind: model.AssetBGM, Name: t.Title, Ext: ext, Origin: "library", License: "commercial",
				Tags: t.Tags, Listed: true, Description: musicDescription(t),
				Meta: map[string]any{"libraryId": t.ID, "category": t.Category, "categoryName": t.CategoryName, "artist": t.Artist, "source": t.Source,
					"page": t.Page, "license": t.License, "licenseUrl": t.LicenseURL, "attribution": false},
			}, data)
		}
		if err != nil {
			failed++
			if len(failures) < 20 {
				failures = append(failures, t.Title+"："+err.Error())
			}
			slog.Warn("music import", "track", t.Title, "err", err)
		} else {
			imported++
		}
	}
	if imported > 0 {
		a.retirePlaceholderMusic()
	}
	jc.SetResult(map[string]any{"imported": imported, "failed": failed, "failures": failures, "total": len(cat.Tracks)})
	msg := fmt.Sprintf("导入完成：新增 %d 首", imported)
	if failed > 0 {
		msg += fmt.Sprintf("，%d 首下载失败（可稍后再次导入补全）", failed)
	}
	jc.Progress(1, msg)
	if len(blocked) > 0 {
		return jobs.Permanent(fmt.Errorf("新增 %d 首；%w", imported, errRobotPolicy))
	}
	if imported == 0 && failed > 0 {
		return jobs.Permanent(errors.New("全部下载失败，请检查服务器能否访问 upload.wikimedia.org 与 opengameart.org"))
	}
	return nil
}

func musicDescription(t MusicTrack) string {
	lic := "CC0 公共领域贡献，可免费商用，无需署名"
	if t.License != "CC0" {
		lic = "公有领域，可免费商用，无需署名"
	}
	who := t.Artist
	if who == "" {
		who = "佚名"
	}
	return truncateStr(fmt.Sprintf("%s · %s · %s（来源：%s）", t.CategoryName, who, lic, t.Source), 490)
}

// downloadMusic fetches a track (MP3 transcode first, the original file as a fallback).
func downloadMusic(ctx context.Context, client *http.Client, ua string, t MusicTrack) ([]byte, string, error) {
	urls := []string{t.URL}
	if t.Original != "" && t.Original != t.URL {
		urls = append(urls, t.Original)
	}
	var lastErr error
	for _, u := range urls {
		host := ""
		if pu, err := neturl.Parse(u); err == nil {
			host = pu.Host
		}
		gap := 300 * time.Millisecond
		if strings.HasSuffix(host, "wikimedia.org") {
			gap = 1500 * time.Millisecond // stay far below Wikimedia's bot limits
		}
		for attempt := 0; attempt < 3; attempt++ {
			if err := libPace.wait(ctx, host, gap); err != nil {
				return nil, "", err
			}
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
			if err != nil {
				return nil, "", err
			}
			req.Header.Set("User-Agent", ua)
			resp, err := client.Do(req)
			retryAfter := time.Duration(attempt+1) * 3 * time.Second
			if err == nil {
				data, rerr := io.ReadAll(io.LimitReader(resp.Body, 120<<20))
				resp.Body.Close()
				switch {
				case rerr != nil:
					err = rerr
				case resp.StatusCode == http.StatusForbidden && strings.Contains(string(data), "robot policy"):
					return nil, "", errRobotPolicy
				case resp.StatusCode == http.StatusTooManyRequests:
					err = fmt.Errorf("源站限流（HTTP 429）")
					if sec, perr := strconv.Atoi(resp.Header.Get("Retry-After")); perr == nil && sec > 0 {
						retryAfter = time.Duration(min(sec, 120)) * time.Second
					}
				case resp.StatusCode != http.StatusOK:
					err = fmt.Errorf("HTTP %d", resp.StatusCode)
				case len(data) < 1024:
					err = fmt.Errorf("文件过小")
				default:
					ext := strings.ToLower(path.Ext(strings.Split(u, "?")[0]))
					if ext == "" || len(ext) > 5 {
						ext = ".mp3"
					}
					return data, strings.TrimPrefix(ext, "."), nil
				}
			}
			lastErr = err
			select {
			case <-ctx.Done():
				return nil, "", ctx.Err()
			case <-time.After(retryAfter):
			}
		}
	}
	return nil, "", lastErr
}

// retirePlaceholderMusic hides the synthesized demo cues once real recordings exist and moves the
// built-in intro/outro templates onto a calm library track. Existing mixes keep working.
func (a *App) retirePlaceholderMusic() {
	var n int64
	a.DB.Model(&model.Asset{}).Where("scope = ? AND kind = ? AND origin = ? AND listed = ?", model.ScopeSystem, model.AssetBGM, "library", true).Count(&n)
	if n < 30 {
		return
	}
	var generated []string
	a.DB.Model(&model.Asset{}).Where("scope = ? AND kind = ? AND origin = ?", model.ScopeSystem, model.AssetBGM, "system").Pluck("id", &generated)
	if len(generated) == 0 {
		return
	}
	a.DB.Model(&model.Asset{}).Where("id IN ?", generated).Update("listed", false)
	if id := a.libraryTrack("温馨治愈", "templates"); id != "" {
		a.DB.Model(&model.AudioTemplate{}).Where("scope = ? AND music_asset_id IN ?", model.ScopeSystem, generated).Update("music_asset_id", id)
	}
}

// libraryTrack picks a library track carrying a tag, spread deterministically by key.
func (a *App) libraryTrack(tag, key string) string {
	var ids []string
	a.DB.Model(&model.Asset{}).Where("scope = ? AND kind = ? AND listed = ? AND "+dbx.Like(a.DB, "tags"), model.ScopeSystem, model.AssetBGM, true, "%\""+tag+"\"%").
		Order("CASE WHEN origin = 'library' THEN 0 ELSE 1 END, created_at asc").Pluck("id", &ids)
	if len(ids) == 0 {
		return ""
	}
	h := fnv.New32a()
	h.Write([]byte(key))
	return ids[int(h.Sum32())%len(ids)]
}

// bgmPreferences maps words found in a book's genre and tags to music categories, best first.
var bgmPreferences = []struct {
	words string
	tags  []string
}{
	{"恐怖 惊悚 灵异 鬼 盗墓 怪谈 民间故事", []string{"恐怖惊悚", "悬疑推理"}},
	{"悬疑 推理 侦探 刑侦 犯罪 谍战 探案", []string{"悬疑推理", "影视配乐"}},
	{"科幻 末世 星际 机甲 赛博", []string{"科幻未来", "氛围空灵"}},
	{"仙侠 修真 古风 国风 玄幻", []string{"古风东方", "冒险奇幻", "史诗热血"}},
	{"奇幻 魔法 西幻", []string{"冒险奇幻", "史诗热血"}},
	{"武侠 江湖", []string{"古风东方", "史诗热血"}},
	{"战争 军事 热血 历史 权谋", []string{"史诗热血", "古典音乐"}},
	{"宫斗 宫廷 古言", []string{"古风东方", "古典音乐"}},
	{"民国 年代", []string{"爵士复古", "经典名曲"}},
	{"助眠 睡前 冥想 白噪音 晚安", []string{"助眠冥想", "氛围空灵"}},
	{"情感 电台 散文 诗歌 朗诵 美文", []string{"钢琴独奏", "经典名曲", "温馨治愈"}},
	{"冒险 探险 游戏", []string{"冒险奇幻", "轻快日常"}},
	{"虐 悲剧 伤感 催泪", []string{"伤感催泪", "钢琴独奏"}},
	{"言情 甜宠 恋爱 爱情 青春 校园", []string{"浪漫言情", "钢琴独奏"}},
	{"儿童 童话 绘本 寓言 亲子", []string{"童话儿童", "轻快日常"}},
	{"喜剧 搞笑 沙雕 段子 相声", []string{"诙谐喜剧", "轻快日常"}},
	{"都市 职场 现代 商战 财经 知识 新闻", []string{"都市轻松", "爵士复古"}},
	{"治愈 日常 美食 田园 种田", []string{"温馨治愈", "民谣田园"}},
}

// syncLibraryAssets keeps imported library items in line with the embedded catalogs after an upgrade:
// renamed categories and titles are applied, and items the catalog no longer lists (e.g. rejected
// orchestra stems) are taken off the shelf. Mixes that already use them keep working.
func (a *App) syncLibraryAssets() {
	type entry struct {
		name, category, categoryName string
		tags                         []string
	}
	want := map[string]entry{}
	for _, t := range MusicCatalog().Tracks {
		want["bgm:"+t.ID] = entry{t.Title, t.Category, t.CategoryName, t.Tags}
	}
	for _, s := range SfxCatalog().Sounds {
		want[s.Kind+":"+s.ID] = entry{s.Name, s.Category, s.CategoryName, s.Tags}
	}
	var assets []model.Asset
	a.DB.Where("scope = ? AND origin = ? AND kind IN ?", model.ScopeSystem, "library", []string{model.AssetBGM, model.AssetSFX, model.AssetAmbience}).Find(&assets)
	var retired, updated int
	for _, as := range assets {
		id, _ := as.Meta.V["libraryId"].(string)
		if id == "" {
			continue
		}
		e, ok := want[as.Kind+":"+id]
		if !ok {
			if as.Listed {
				a.DB.Model(&model.Asset{}).Where("id = ?", as.ID).Update("listed", false)
				retired++
			}
			continue
		}
		meta := as.Meta.V
		if meta == nil {
			meta = map[string]any{}
		}
		if as.Name == e.name && strings.Join(as.Tags.V, ",") == strings.Join(e.tags, ",") && meta["categoryName"] == e.categoryName && meta["category"] == e.category {
			continue
		}
		meta["category"], meta["categoryName"] = e.category, e.categoryName
		a.DB.Model(&model.Asset{}).Where("id = ?", as.ID).Updates(map[string]any{"name": e.name, "tags": model.Strings(e.tags...), "meta": model.Map{V: meta}})
		updated++
	}
	if retired+updated > 0 {
		slog.Info("library assets synced with catalog", "updated", updated, "retired", retired)
	}
}
