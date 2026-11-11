package audio

import (
	"context"
	"math"
	"sort"
	"strings"
)

// Effect is a named ffmpeg filter preset applied to a clip or track before mixing.
type Effect struct {
	Key    string `json:"key"`
	Name   string `json:"name"`
	Desc   string `json:"desc"`
	Filter string `json:"-"`
	Group  string `json:"group"`
}

var Effects = []Effect{
	{"denoise", "降噪", "压低底噪与环境噪声", "afftdn=nf=-28", "修复"},
	{"deess", "去齿音", "削弱刺耳的“嘶”声", "deesser=i=0.6", "修复"},
	{"telephone", "电话音", "模拟电话听筒的窄带声音", "highpass=f=320,lowpass=f=3300,acompressor=threshold=-22dB:ratio=4:attack=5:release=60,volume=1.6", "场景"},
	{"radio", "对讲机 / 广播", "带失真的窄带通讯效果", "highpass=f=450,lowpass=f=2800,acompressor=threshold=-24dB:ratio=6,volume=1.8", "场景"},
	{"room", "房间混响", "小房间里的近距离空间感", "aecho=0.85:0.6:35|62:0.22|0.14", "空间"},
	{"hall", "大厅混响", "空旷大厅的回响", "aecho=0.85:0.75:90|170|260:0.3|0.22|0.14", "空间"},
	{"valley", "山谷回声", "远处清晰可辨的回声", "aecho=0.8:0.85:420|840:0.35|0.18", "空间"},
	{"monologue", "内心独白", "轻微混响 + 柔化，适合心理活动", "aecho=0.8:0.6:24|48:0.2|0.12,lowpass=f=9000,volume=0.92", "场景"},
	{"dream", "梦境 / 回忆", "朦胧、带空间感的回忆效果", "aecho=0.8:0.7:60|120:0.28|0.18,lowpass=f=6500,chorus=0.6:0.9:50:0.4:0.25:2", "场景"},
	{"pitch_up", "升调", "让声音更年轻、轻快", "asetrate=44100*1.1,aresample=44100,atempo=0.909", "变声"},
	{"pitch_down", "降调", "让声音更低沉、成熟", "asetrate=44100*0.9,aresample=44100,atempo=1.111", "变声"},
	{"monster", "怪物 / 非人", "低沉粗粝的非人声线", "asetrate=44100*0.78,aresample=44100,atempo=1.28,aecho=0.8:0.5:20:0.3", "变声"},
	{"warm", "温暖", "增强低中频，声音更饱满", "equalizer=f=180:t=q:w=1:g=3,equalizer=f=6500:t=q:w=1:g=-2", "音色"},
	{"bright", "明亮", "提升清晰度和穿透力", "equalizer=f=4200:t=q:w=1:g=3,equalizer=f=250:t=q:w=1:g=-1.5", "音色"},
	{"limiter", "限幅", "防止爆音", "alimiter=limit=0.89", "修复"},
}

func EffectByKey(k string) (Effect, bool) {
	for _, e := range Effects {
		if e.Key == k {
			return e, true
		}
	}
	return Effect{}, false
}

// FilterChain builds an ffmpeg -af chain from effect keys.
func FilterChain(keys []string) string {
	var parts []string
	for _, k := range keys {
		if e, ok := EffectByKey(k); ok {
			parts = append(parts, e.Filter)
		}
	}
	return strings.Join(parts, ",")
}

type MixClip struct {
	Source     string
	StartMs    int64
	OffsetMs   int64
	DurationMs int64 // 0 = rest of source
	Loop       bool
	GainDB     float64
	FadeInMs   int64
	FadeOutMs  int64
	Filter     string
	Duck       bool // attenuated while dialogue plays
	Voice      bool // dialogue: triggers ducking
}

type MixOptions struct {
	DuckDB  float64
	TotalMs int64
}

type loadedSource struct {
	pcm     *PCM
	lastEnd int // last frame (timeline) where it is needed
}

// Mix renders clips into a 44.1 kHz stereo WAV using a block-streaming mixer, so memory stays
// proportional to the clips currently playing rather than the chapter length.
func (f *FF) Mix(ctx context.Context, clips []MixClip, out string, opt MixOptions) (int64, error) {
	const rate = SampleRate
	type prepared struct {
		MixClip
		start, end, offset int
		key                string
	}
	srcFrames := map[string]int{}
	var list []prepared
	var totalFrames int
	for _, c := range clips {
		key := c.Source + "|" + c.Filter
		p := prepared{MixClip: c, key: key, start: int(c.StartMs * rate / 1000), offset: int(c.OffsetMs * rate / 1000)}
		if c.DurationMs > 0 {
			p.end = p.start + int(c.DurationMs*rate/1000)
		} else {
			// need the source length
			n, ok := srcFrames[key]
			if !ok {
				info, err := f.Probe(ctx, c.Source)
				if err != nil {
					return 0, err
				}
				n = int(info.DurationMs * rate / 1000)
				srcFrames[key] = n
			}
			p.end = p.start + n - p.offset
		}
		if p.end <= p.start {
			continue
		}
		list = append(list, p)
		if p.end > totalFrames {
			totalFrames = p.end
		}
	}
	if opt.TotalMs > 0 {
		totalFrames = int(opt.TotalMs * rate / 1000)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].start < list[j].start })
	lastUse := map[string]int{}
	for _, p := range list {
		if p.end > lastUse[p.key] {
			lastUse[p.key] = p.end
		}
	}
	var voice [][2]int
	for _, p := range list {
		if p.Voice {
			voice = append(voice, [2]int{p.start, p.end})
		}
	}
	w, err := CreateStreamWAV(out, rate, 2)
	if err != nil {
		return 0, err
	}
	cache := map[string]*loadedSource{}
	duckLin := float64(DB(opt.DuckDB))
	if opt.DuckDB == 0 {
		duckLin = float64(DB(-12))
	}
	attack := math.Exp(-1.0 / (0.08 * rate))
	release := math.Exp(-1.0 / (0.45 * rate))
	duckGain := 1.0
	vi := 0
	next := 0
	var active []int
	const block = 4096
	buf := make([]float32, block*2)
	duck := make([]float64, block)
	for b0 := 0; b0 < totalFrames; b0 += block {
		if ctx.Err() != nil {
			w.Close()
			return 0, ctx.Err()
		}
		b1 := b0 + block
		if b1 > totalFrames {
			b1 = totalFrames
		}
		n := b1 - b0
		for i := range buf[:n*2] {
			buf[i] = 0
		}
		// ducking envelope
		for t := 0; t < n; t++ {
			fr := b0 + t
			for vi < len(voice) && voice[vi][1] <= fr {
				vi++
			}
			speaking := false
			for k := vi; k < len(voice) && voice[k][0] <= fr; k++ {
				if fr < voice[k][1] {
					speaking = true
					break
				}
			}
			target := 1.0
			if speaking {
				target = duckLin
			}
			coef := release
			if target < duckGain {
				coef = attack
			}
			duckGain = target + (duckGain-target)*coef
			duck[t] = duckGain
		}
		for next < len(list) && list[next].start < b1 {
			active = append(active, next)
			next++
		}
		kept := active[:0]
		for _, idx := range active {
			c := &list[idx]
			if c.end <= b0 {
				continue
			}
			kept = append(kept, idx)
			src, ok := cache[c.key]
			if !ok {
				pcm, err := f.DecodePCM(ctx, c.Source, rate, 2, c.Filter)
				if err != nil {
					w.Close()
					return 0, err
				}
				src = &loadedSource{pcm: pcm}
				cache[c.key] = src
			}
			frames := src.pcm.Frames()
			if frames == 0 {
				continue
			}
			g := float64(DB(c.GainDB))
			lo, hi := b0, b1
			if c.start > lo {
				lo = c.start
			}
			if c.end < hi {
				hi = c.end
			}
			fadeIn := int(c.FadeInMs * rate / 1000)
			fadeOut := int(c.FadeOutMs * rate / 1000)
			for fr := lo; fr < hi; fr++ {
				si := fr - c.start + c.offset
				if c.Loop {
					si %= frames
				} else if si >= frames {
					break
				}
				gain := g
				if fadeIn > 0 && fr-c.start < fadeIn {
					gain *= float64(fr-c.start) / float64(fadeIn)
				}
				if fadeOut > 0 && c.end-fr < fadeOut {
					gain *= float64(c.end-fr) / float64(fadeOut)
				}
				if c.Duck {
					gain *= duck[fr-b0]
				}
				o := (fr - b0) * 2
				buf[o] += src.pcm.Data[si*2] * float32(gain)
				buf[o+1] += src.pcm.Data[si*2+1] * float32(gain)
			}
		}
		active = kept
		// release decoded sources that are no longer needed
		for k, s := range cache {
			if lastUse[k] <= b1 {
				_ = s
				delete(cache, k)
			}
		}
		for i := range buf[:n*2] {
			buf[i] = softClip(buf[i])
		}
		w.Write(buf[:n*2])
	}
	if err := w.Close(); err != nil {
		return 0, err
	}
	return int64(totalFrames) * 1000 / rate, nil
}

func softClip(x float32) float32 {
	const knee = 0.95
	a := x
	if a < 0 {
		a = -a
	}
	if a <= knee {
		return x
	}
	y := knee + (1-knee)*float32(math.Tanh(float64((a-knee)/(1-knee))))
	if x < 0 {
		return -y
	}
	return y
}
