package core

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"voicebook/internal/audio"
	"voicebook/internal/model"
)

func wavBytes(t *testing.T, p *audio.PCM) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := audio.EncodeWAV16(&b, p); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// tone of `secs` seconds, silence-padded on both sides
func paddedTone(secs, pad float64, freq float64) *audio.PCM {
	const rate = 44100
	n := int((secs + 2*pad) * rate)
	p := &audio.PCM{Rate: rate, Channels: 1, Data: make([]float32, n)}
	for i := int(pad * rate); i < int((pad+secs)*rate); i++ {
		p.Data[i] = float32(0.3 * math.Sin(2*math.Pi*freq*float64(i)/rate))
	}
	return p
}

func TestRenderSfx(t *testing.T) {
	a, _ := testApp(t)
	a.FF = &audio.FF{FFmpeg: "ffmpeg", FFprobe: "ffprobe"}
	if !a.FF.Available() {
		t.Skip("ffmpeg not installed")
	}
	var zb bytes.Buffer
	zw := zip.NewWriter(&zb)
	for name, p := range map[string]*audio.PCM{"steps/a.wav": paddedTone(0.08, 0.2, 180), "steps/b.wav": paddedTone(0.1, 0.1, 140), "bed.wav": paddedTone(20, 0, 220)} {
		w, _ := zw.Create(name)
		w.Write(wavBytes(t, p))
	}
	zw.Close()
	long := wavBytes(t, paddedTone(1.5, 2, 440))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/pack.zip":
			w.Write(zb.Bytes())
		case "/knock.wav":
			w.Write(long)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	dir := t.TempDir()
	dl := &sfxDownloads{client: srv.Client(), dir: dir, files: map[string]string{}}
	ctx := context.Background()
	probe := func(data []byte) *audio.ProbeInfo {
		t.Helper()
		p := filepath.Join(dir, "probe.mp3")
		os.WriteFile(p, data, 0o644)
		info, err := a.FF.Probe(ctx, p)
		if err != nil {
			t.Fatal(err)
		}
		return info
	}

	// one-shot effect: leading/trailing silence trimmed (1.5 s tone padded with 2 s of silence), short clips peak-normalised
	knock, err := a.renderSfx(ctx, dl, SfxSound{ID: "k", Name: "敲门", Kind: model.AssetSFX, URL: srv.URL + "/knock.wav"})
	if err != nil {
		t.Fatal(err)
	}
	if d := probe(knock).DurationMs; d < 1650 || d > 1950 {
		t.Fatalf("trimmed length %d ms, want about 1.5 s + short tail", d)
	}
	p := filepath.Join(dir, "knock.mp3")
	os.WriteFile(p, knock, 0o644)
	if peak, _ := a.FF.MaxVolume(ctx, p); peak < -8 || peak > -4 {
		t.Fatalf("short effects are peak-normalised to about -6 dBFS, got %.1f", peak)
	}

	// ambience bed from a zip member: loudness-normalised
	start, dur := int64(1000), int64(15000)
	bed, err := a.renderSfx(ctx, dl, SfxSound{ID: "b", Name: "环境", Kind: model.AssetAmbience, URL: srv.URL + "/pack.zip", Member: "bed.wav", Start: &start, Dur: &dur})
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(p, bed, 0o644)
	if m, err := a.FF.MeasureLoudness(ctx, p, -26, -1.5); err != nil || math.Abs(m.InputI-ambienceTargetLUFS) > 1.5 {
		t.Fatalf("ambience loudness %+v (%v), want about %.0f LUFS", m, err, ambienceTargetLUFS)
	}
	if dl.n != 3 { // knock.wav, pack.zip, bed.wav extracted: the pack is downloaded once
		t.Fatalf("downloads+extractions = %d", dl.n)
	}
	if d := probe(bed).DurationMs; d < 14500 || d > 15500 {
		t.Fatalf("explicit window gives %d ms, want 15 s", d)
	}

	// loudest stretch of a long recording
	if st, du, err := a.chooseClip(ctx, filepath.Join(dir, "src0001.wav"), SfxSound{Kind: model.AssetSFX, PickMs: 1000}); err != nil || st < 1900 || st > 2600 || du > 1300 {
		t.Fatalf("pick loudest: start %d dur %d err %v", st, du, err)
	}

	// footsteps assembled from single steps
	steps, err := a.renderSfx(ctx, dl, SfxSound{ID: "s", Name: "走路", Kind: model.AssetSFX, URL: srv.URL + "/pack.zip", Steps: &SfxSteps{Members: []string{"steps/a.wav", "steps/b.wav"}, IntervalMs: 500, Count: 6}})
	if err != nil {
		t.Fatal(err)
	}
	if d := probe(steps).DurationMs; d < 2500 || d > 3200 {
		t.Fatalf("6 steps at 500 ms should last about 2.6-3 s, got %d ms", d)
	}

	if _, err := a.renderSfx(ctx, dl, SfxSound{ID: "x", Name: "缺失", Kind: model.AssetSFX, URL: srv.URL + "/pack.zip", Member: "nope.wav", DurationMs: 1000}); err == nil {
		t.Fatal("a missing zip member must fail")
	}
}

func TestRankSfx(t *testing.T) {
	mk := func(name string, tags ...string) model.Asset {
		return model.Asset{Base: model.Base{ID: name}, Name: name, Tags: model.Strings(tags...), Origin: "library"}
	}
	lib := []model.Asset{
		mk("门铃叮咚", "门铃", "叮咚", "门窗开关"), mk("敲门", "敲门", "门", "咚咚", "门窗开关"), mk("狗叫", "狗", "狗叫", "汪汪", "动物鸣叫"),
		mk("狼嚎", "狼", "狼嚎", "夜晚", "动物鸣叫"), mk("雷雨交加", "雷雨", "打雷", "下雨", "风雨雷电"), mk("惊雷炸响", "雷声", "炸雷", "风雨雷电"),
	}
	top := func(cue string) string {
		r := rankSfx(cue, lib, 3)
		if len(r) == 0 {
			return ""
		}
		return r[0].Name
	}
	for cue, want := range map[string]string{"急促的敲门声": "敲门", "远处传来几声狗叫": "狗叫", "一声炸雷": "惊雷炸响", "门铃响了": "门铃叮咚", "夜里狼嚎声此起彼伏": "狼嚎"} {
		if got := top(cue); got != want {
			t.Errorf("%s -> %s, want %s", cue, got, want)
		}
	}
	if r := rankSfx("她笑了笑", lib, 3); len(r) != 0 {
		t.Errorf("unrelated cue should match nothing, got %v", r[0].Name)
	}
	if keywordAsset("窗外下雨", lib) == "" {
		t.Error("keyword fallback should find the rain bed")
	}
}

func TestDownloadHonoursRobotPolicy(t *testing.T) {
	hits := 0
	var gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		gotUA = r.Header.Get("User-Agent")
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte("Please honor our robot policy https://w.wiki/4wJS. (abc)"))
	}))
	defer srv.Close()
	a, _ := testApp(t)
	a.Cfg.PublicURL, a.Cfg.BotContact = "https://books.example.org", "ops@example.org"
	_, _, err := downloadMusic(context.Background(), srv.Client(), a.botUA(), MusicTrack{URL: srv.URL + "/a.ogg"})
	if !errors.Is(err, errRobotPolicy) {
		t.Fatalf("want errRobotPolicy, got %v", err)
	}
	if hits != 1 {
		t.Fatalf("a robot-policy refusal must not be retried, got %d requests", hits)
	}
	if !strings.Contains(gotUA, "https://books.example.org") || !strings.Contains(gotUA, "ops@example.org") {
		t.Fatalf("user agent must carry contact details: %q", gotUA)
	}
}
