package gen

import (
	"context"
	"encoding/base64"
	"net/http"
	"strings"
	"testing"
)

func TestVolcSFXPrompt(t *testing.T) {
	if got := VolcSFXPrompt("雨夜，屋檐滴水。", 8000, false, 0, true); got != "雨夜，屋檐滴水。只有音效和环境声，没有人声和背景音乐，总时长约 8 秒。" {
		t.Fatalf("prompt: %s", got)
	}
	if got := VolcSFXPrompt("远处的雷声", 0, true, 2, true); !strings.HasPrefix(got, "参考@音频1、@音频2的声音，远处的雷声。") || !strings.Contains(got, "循环播放") {
		t.Fatalf("references and loop: %s", got)
	}
	// a description that asks for speech keeps it
	if got := VolcSFXPrompt("集市里有人吆喝：“卖糖葫芦”", 0, false, 0, true); strings.Contains(got, "没有人声") {
		t.Fatalf("speech requested: %s", got)
	}
	named := VolcSFXPrompt("参考 @音频1 加入雷声", 0, false, 1, true)
	if strings.HasPrefix(named, "参考@音频1的声音") {
		t.Fatalf("a description that already names the reference is kept as written: %s", named)
	}
	// naming the clip or not makes no difference to the "effects only" hint
	unnamed := VolcSFXPrompt("加入雷声", 0, false, 1, true)
	if !strings.Contains(named, "只有音效和环境声") || !strings.Contains(unnamed, "只有音效和环境声") {
		t.Fatalf("effects-only hint must not depend on naming the clip:\n%s\n%s", named, unnamed)
	}
	if got := VolcSFXPrompt("@音频1 的旋律换成钢琴", 0, false, 1, true); strings.Contains(got, "没有人声和背景音乐") {
		t.Fatalf("music asked for in words: %s", got)
	}
}

func TestVolcSFXRequest(t *testing.T) {
	var got map[string]any
	var key string
	srv := fakeEleven(t, func(w http.ResponseWriter, r *http.Request, body map[string]any) {
		if r.URL.Path != "/api/v3/tts/create" {
			w.WriteHeader(404)
			return
		}
		got, key = body, r.Header.Get("X-Api-Key")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"audio":"` + base64.StdEncoding.EncodeToString(make([]byte, 4096)) + `","duration":6.2,"original_duration":6.2}`))
	})
	p, err := NewSFX(Account{Provider: "volcengine_audio", BaseURL: srv.URL, APIKey: "vk"})
	if err != nil {
		t.Fatal(err)
	}
	f, err := p.GenerateSFX(context.Background(), SFXRequest{Prompt: "木门吱呀一声被推开", DurationMs: 6000, References: [][]byte{[]byte("RIFFwav")}})
	if err != nil {
		t.Fatal(err)
	}
	if key != "vk" || got["model"] != "seed-audio-1.0" || !strings.Contains(got["text_prompt"].(string), "参考@音频1的声音，木门吱呀一声被推开") {
		t.Fatalf("request: %v", got)
	}
	refs := got["references"].([]any)
	if len(refs) != 1 || refs[0].(map[string]any)["audio_data"] != base64.StdEncoding.EncodeToString([]byte("RIFFwav")) {
		t.Fatalf("references: %v", refs)
	}
	if cfg := got["audio_config"].(map[string]any); cfg["format"] != "mp3" {
		t.Fatalf("audio_config: %v", cfg)
	}
	if f.Ext != "mp3" || len(f.Data) != 4096 || f.Meta["billedSeconds"] != 6.2 || f.Meta["provider"] != "volcengine_audio" {
		t.Fatalf("file: %s %d %v", f.Ext, len(f.Data), f.Meta)
	}
	if _, err := p.GenerateSFX(context.Background(), SFXRequest{Prompt: "x", References: make([][]byte, 4)}); err == nil {
		t.Fatal("at most three references")
	}
}
