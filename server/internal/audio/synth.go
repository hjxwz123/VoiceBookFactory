package audio

import (
	"hash/fnv"
	"math"
	"math/rand"
	"strings"
	"unicode"
)

// Seed derives a deterministic seed from strings.
func Seed(parts ...string) int64 {
	h := fnv.New64a()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return int64(h.Sum64() & 0x7fffffffffffffff)
}

// ---------------------------------------------------------------------------
// Placeholder voice: a formant "babble" synthesizer. It is NOT speech synthesis; it produces
// syllable-like audio whose rhythm, pitch and timbre follow the text, the voice and the emotion,
// so that generation, mixing, alignment of timings and exports can be exercised end to end
// before a real TTS provider is connected.
// ---------------------------------------------------------------------------

type VoiceStyle struct {
	Gender    string
	AgeGroup  string
	Seed      int64
	Speed     float64 // 1 = normal
	Pitch     float64 // semitones
	Volume    float64 // dB
	Emotion   string
	Intensity int
}

var vowels = [][3]float64{
	{800, 1200, 2500}, // a
	{500, 900, 2400},  // o
	{520, 1750, 2500}, // e
	{300, 2300, 3000}, // i
	{330, 800, 2300},  // u
	{300, 1850, 2300}, // ü
}

func Babble(text string, st VoiceStyle) *PCM {
	rate := SampleRate
	rng := rand.New(rand.NewSource(st.Seed))
	f0 := 160.0
	formantShift := 1.0
	switch st.Gender {
	case "male":
		f0 = 112
	case "female":
		f0, formantShift = 212, 1.14
	}
	switch st.AgeGroup {
	case "child":
		f0 *= 1.38
		formantShift *= 1.22
	case "teen":
		f0 *= 1.16
		formantShift *= 1.08
	case "young":
		f0 *= 1.05
	case "middle":
		f0 *= 0.94
	case "senior":
		f0 *= 0.9
		formantShift *= 0.97
	}
	// per-voice timbre
	f0 *= 0.92 + rng.Float64()*0.16
	formantShift *= 0.94 + rng.Float64()*0.12
	breath := 0.03 + rng.Float64()*0.06
	vibrato := 0.004 + rng.Float64()*0.006
	speed := st.Speed
	if speed <= 0 {
		speed = 1
	}
	amp := 1.0
	contourRange := 0.12
	whisper := false
	inten := float64(st.Intensity)
	if inten == 0 {
		inten = 2
	}
	k := 0.5 + inten*0.25
	switch st.Emotion {
	case "happy":
		f0 *= 1 + 0.08*k
		speed *= 1 + 0.06*k
		contourRange *= 1 + 0.5*k
	case "sad":
		f0 *= 1 - 0.07*k
		speed *= 1 - 0.1*k
		amp *= 1 - 0.12*k
		contourRange *= 0.6
	case "angry":
		amp *= 1 + 0.15*k
		speed *= 1 + 0.05*k
		f0 *= 1 + 0.04*k
	case "fearful", "nervous":
		f0 *= 1 + 0.06*k
		vibrato *= 2.5
		speed *= 1 + 0.05*k
	case "surprised":
		f0 *= 1 + 0.1*k
		contourRange *= 1.6
	case "gentle", "tender":
		amp *= 0.86
		speed *= 0.95
	case "whisper":
		whisper = true
		amp *= 0.6
	case "shout":
		amp *= 1.25
		f0 *= 1.14
	case "tired":
		speed *= 0.85
		f0 *= 0.93
		amp *= 0.85
	}
	f0 *= math.Pow(2, st.Pitch/12)
	amp *= math.Pow(10, st.Volume/20)

	var out []float32
	silence := func(msv float64) {
		n := int(msv / 1000 * float64(rate) / speed)
		for i := 0; i < n; i++ {
			out = append(out, float32((rng.Float64()*2-1)*0.0004))
		}
	}
	phases := make([]float64, 16)
	prevF := f0
	syllable := func(stress float64) {
		dur := (0.165 + rng.Float64()*0.05) / speed
		n := int(dur * float64(rate))
		v := vowels[rng.Intn(len(vowels))]
		shape := rng.Intn(4)
		target := f0 * (1 + (rng.Float64()*2-1)*contourRange*0.6)
		harm := make([]float64, 16)
		for h := 1; h < 16; h++ {
			freq := float64(h) * target
			var g float64
			for fi, fc := range v {
				fc *= formantShift
				bw := 90.0 + float64(fi)*60
				g += math.Exp(-math.Pow((freq-fc)/bw, 2)/2) * (1 - float64(fi)*0.25)
			}
			harm[h] = (0.08 + g) / math.Pow(float64(h), 0.9)
		}
		onset := int((0.008 + rng.Float64()*0.02) * float64(rate))
		lp := 0.0
		for i := 0; i < n; i++ {
			t := float64(i) / float64(n)
			var c float64
			switch shape {
			case 0:
				c = 1
			case 1:
				c = 1 - contourRange*0.5 + contourRange*t
			case 2:
				c = 1 - contourRange*math.Sin(math.Pi*t)
			default:
				c = 1 + contourRange*0.5 - contourRange*t
			}
			fNow := prevF + (target*c-prevF)*math.Min(1, t*6)
			fNow *= 1 + vibrato*math.Sin(2*math.Pi*5.3*float64(i)/float64(rate))
			env := math.Min(1, float64(i)/(0.012*float64(rate))) * math.Min(1, float64(n-i)/(0.04*float64(rate))) * (1 - 0.25*t)
			var s float64
			if !whisper {
				for h := 1; h < 16; h++ {
					phases[h] += 2 * math.Pi * fNow * float64(h) / float64(rate)
					if phases[h] > 2*math.Pi {
						phases[h] -= 2 * math.Pi
					}
					s += harm[h] * math.Sin(phases[h])
				}
				s *= 0.5
			}
			noise := rng.Float64()*2 - 1
			lp += 0.25 * (noise - lp)
			nz := (noise - lp) * breath
			if whisper {
				nz = (noise - lp) * 0.35
			}
			if i < onset {
				nz += (noise - lp) * 0.25 * (1 - float64(i)/float64(onset))
			}
			out = append(out, float32((s+nz)*env*amp*stress*0.45))
		}
		prevF = target
	}
	words := 0
	for _, r := range text {
		switch {
		case strings.ContainsRune("，、,", r):
			silence(210)
		case strings.ContainsRune("；;：:", r):
			silence(280)
		case strings.ContainsRune("。！？!?…", r):
			silence(380)
		case strings.ContainsRune("—-~～", r):
			silence(140)
		case unicode.IsSpace(r):
			silence(55)
		case unicode.Is(unicode.Han, r) || unicode.IsDigit(r):
			stress := 1.0
			if words%4 == 0 {
				stress = 1.12
			}
			syllable(stress)
			words++
		case unicode.IsLetter(r):
			// roughly one syllable per three latin letters
			if words%3 == 0 {
				syllable(1)
			}
			words++
		}
	}
	if len(out) == 0 {
		silence(300)
	}
	p := &PCM{Rate: rate, Channels: 1, Data: out}
	NormalizeRMS(p, -20+st.Volume, -2)
	return p
}

// ---------------------------------------------------------------------------
// DSP helpers
// ---------------------------------------------------------------------------

type biquad struct{ b0, b1, b2, a1, a2, x1, x2, y1, y2 float64 }

func bandpass(freq, q float64) *biquad {
	w := 2 * math.Pi * freq / SampleRate
	alpha := math.Sin(w) / (2 * q)
	a0 := 1 + alpha
	return &biquad{b0: alpha / a0, b1: 0, b2: -alpha / a0, a1: -2 * math.Cos(w) / a0, a2: (1 - alpha) / a0}
}

func lowpassBQ(freq, q float64) *biquad {
	w := 2 * math.Pi * freq / SampleRate
	alpha := math.Sin(w) / (2 * q)
	cw := math.Cos(w)
	a0 := 1 + alpha
	return &biquad{b0: (1 - cw) / 2 / a0, b1: (1 - cw) / a0, b2: (1 - cw) / 2 / a0, a1: -2 * cw / a0, a2: (1 - alpha) / a0}
}

func (f *biquad) do(x float64) float64 {
	y := f.b0*x + f.b1*f.x1 + f.b2*f.x2 - f.a1*f.y1 - f.a2*f.y2
	f.x2, f.x1, f.y2, f.y1 = f.x1, x, f.y1, y
	return y
}

// ---------------------------------------------------------------------------
// Procedural sound effects (placeholder generator for AI SFX)
// ---------------------------------------------------------------------------

type sfxKind struct {
	key   string
	words []string
}

var sfxKinds = []sfxKind{
	{"rain", []string{"雨", "rain"}},
	{"thunder", []string{"雷", "thunder"}},
	{"wind", []string{"风", "wind", "呼啸"}},
	{"knock", []string{"敲门", "knock", "叩门"}},
	{"door", []string{"门", "door", "吱呀"}},
	{"footsteps", []string{"脚步", "footstep", "走路", "步"}},
	{"heartbeat", []string{"心跳", "heart"}},
	{"bell", []string{"钟", "铃", "bell", "chime"}},
	{"clock", []string{"滴答", "钟表", "clock", "tick"}},
	{"fire", []string{"火", "篝火", "fire", "crackle", "柴"}},
	{"water", []string{"水", "溪", "河", "stream", "river", "流"}},
	{"waves", []string{"海", "浪", "ocean", "wave"}},
	{"birds", []string{"鸟", "bird", "鸣"}},
	{"phone", []string{"电话", "phone", "铃声"}},
	{"sword", []string{"剑", "刀", "sword", "金属", "兵器"}},
	{"explosion", []string{"爆炸", "explosion", "轰"}},
	{"hooves", []string{"马", "horse", "蹄"}},
	{"crowd", []string{"人群", "嘈杂", "crowd", "喧闹", "集市"}},
	{"applause", []string{"掌声", "鼓掌", "applause"}},
	{"whoosh", []string{"嗖", "whoosh", "风声", "闪"}},
}

func DetectSFX(prompt string) string {
	p := strings.ToLower(prompt)
	for _, k := range sfxKinds {
		for _, w := range k.words {
			if strings.Contains(p, w) {
				return k.key
			}
		}
	}
	return "whoosh"
}

func GenerateSFX(prompt string, durationMs int, seed int64) (*PCM, string) {
	kind := DetectSFX(prompt)
	if durationMs <= 0 {
		durationMs = 4000
	}
	if durationMs > 20000 {
		durationMs = 20000
	}
	rate := SampleRate
	n := durationMs * rate / 1000
	rng := rand.New(rand.NewSource(seed))
	out := make([]float32, n*2)
	set := func(i int, l, r float64) {
		out[i*2] += float32(l)
		out[i*2+1] += float32(r)
	}
	noise := func() float64 { return rng.Float64()*2 - 1 }
	brown := 0.0
	brownNoise := func() float64 {
		brown = (brown + 0.02*noise()) / 1.02
		return brown * 3.5
	}
	thump := func(at int, freq, decay, amp float64) {
		for i := 0; i < int(0.25*float64(rate)) && at+i < n; i++ {
			t := float64(i) / float64(rate)
			f := freq * (1 + 1.5*math.Exp(-t*40))
			v := math.Sin(2*math.Pi*f*t) * math.Exp(-t*decay) * amp
			v += noise() * math.Exp(-t*90) * amp * 0.3
			set(at+i, v, v)
		}
	}
	switch kind {
	case "rain":
		lp := lowpassBQ(5000, 0.7)
		for i := 0; i < n; i++ {
			v := lp.do(noise()) * 0.18
			set(i, v, v*0.95)
		}
		for d := 0; d < n/400; d++ {
			at := rng.Intn(n)
			pan := rng.Float64()
			for i := 0; i < 300 && at+i < n; i++ {
				v := noise() * math.Exp(-float64(i)/40) * 0.25
				set(at+i, v*pan, v*(1-pan))
			}
		}
	case "thunder":
		lp := lowpassBQ(180, 0.7)
		for i := 0; i < n; i++ {
			t := float64(i) / float64(n)
			env := math.Exp(-t*3.5) * (0.6 + 0.4*math.Sin(t*25+rng.Float64()))
			if t < 0.02 {
				env *= t / 0.02
			}
			v := lp.do(noise()) * env * 2.4
			set(i, v, v)
		}
	case "wind":
		bp := bandpass(500, 0.8)
		for i := 0; i < n; i++ {
			t := float64(i) / float64(rate)
			bp2 := 350 + 280*math.Sin(t*0.7+rng.Float64()*0.01) + 120*math.Sin(t*1.9)
			if i%512 == 0 {
				*bp = *bandpass(bp2, 0.9)
			}
			v := bp.do(brownNoise()+noise()*0.3) * (0.5 + 0.3*math.Sin(t*0.5))
			set(i, v*(0.8+0.2*math.Sin(t)), v*(0.8-0.2*math.Sin(t)))
		}
	case "knock":
		for k := 0; k < 3; k++ {
			thump(int(0.3*float64(rate))+k*int(0.24*float64(rate)), 140, 30, 0.8)
		}
	case "door":
		for i := 0; i < n && i < int(1.4*float64(rate)); i++ {
			t := float64(i) / float64(rate)
			f := 220 + 180*math.Sin(t*3.2) + 40*math.Sin(t*41)
			ph := 2 * math.Pi * f * t
			v := (math.Mod(ph, 2*math.Pi)/math.Pi - 1) * 0.12 * math.Min(1, t*8) * math.Exp(-t*0.8)
			v += noise() * 0.02
			set(i, v, v)
		}
		thump(int(1.4*float64(rate)), 90, 18, 0.9)
	case "footsteps":
		step := int(0.55 * float64(rate))
		for at, k := int(0.1*float64(rate)), 0; at < n; at, k = at+step+rng.Intn(rate/20), k+1 {
			thump(at, 95+float64(k%2)*20, 45, 0.55)
		}
	case "heartbeat":
		for at := int(0.1 * float64(rate)); at < n; at += int(0.85 * float64(rate)) {
			thump(at, 55, 20, 1.0)
			thump(at+int(0.22*float64(rate)), 50, 22, 0.7)
		}
	case "bell":
		partials := []float64{1, 2.0, 2.4, 3.0, 4.2, 5.4}
		base := 520.0 + rng.Float64()*200
		for i := 0; i < n; i++ {
			t := float64(i) / float64(rate)
			var v float64
			for pi, p := range partials {
				v += math.Sin(2*math.Pi*base*p*t) * math.Exp(-t*(1.2+float64(pi)*0.9)) / float64(pi+1)
			}
			v *= 0.35
			set(i, v, v)
		}
	case "clock":
		for at, k := 0, 0; at < n; at, k = at+rate/2, k+1 {
			f := 2400.0
			if k%2 == 1 {
				f = 1900
			}
			for i := 0; i < 600 && at+i < n; i++ {
				t := float64(i) / float64(rate)
				v := math.Sin(2*math.Pi*f*t) * math.Exp(-t*300) * 0.5
				set(at+i, v, v)
			}
		}
	case "fire":
		lp := lowpassBQ(900, 0.7)
		for i := 0; i < n; i++ {
			v := lp.do(brownNoise()) * 0.5
			set(i, v, v)
		}
		for c := 0; c < n/900; c++ {
			at := rng.Intn(n)
			amp := 0.2 + rng.Float64()*0.5
			for i := 0; i < 120 && at+i < n; i++ {
				v := noise() * math.Exp(-float64(i)/18) * amp
				set(at+i, v, v*0.8)
			}
		}
	case "water":
		bp := bandpass(1200, 0.6)
		for i := 0; i < n; i++ {
			t := float64(i) / float64(rate)
			if i%256 == 0 {
				*bp = *bandpass(900+500*math.Sin(t*7.3)+300*math.Sin(t*13.1), 1.2)
			}
			v := bp.do(noise()) * 0.5
			set(i, v, v*0.9)
		}
	case "waves":
		lp := lowpassBQ(1400, 0.7)
		for i := 0; i < n; i++ {
			t := float64(i) / float64(rate)
			env := math.Pow(0.5+0.5*math.Sin(2*math.Pi*t/5.5-1.2), 2)
			v := lp.do(noise()) * (0.08 + 0.5*env)
			set(i, v, v*(0.85+0.15*math.Sin(t)))
		}
	case "birds":
		for c := 0; c < durationMs/350; c++ {
			at := rng.Intn(n)
			f0 := 2600 + rng.Float64()*1600
			l := int((0.06 + rng.Float64()*0.12) * float64(rate))
			pan := rng.Float64()
			for i := 0; i < l && at+i < n; i++ {
				t := float64(i) / float64(l)
				f := f0 * (1 + 0.35*math.Sin(math.Pi*t))
				v := math.Sin(2*math.Pi*f*float64(i)/float64(rate)) * math.Sin(math.Pi*t) * 0.25
				set(at+i, v*pan, v*(1-pan))
			}
		}
	case "phone":
		for at := 0; at < n; at += 3 * rate {
			for i := 0; i < 2*rate && at+i < n; i++ {
				t := float64(i) / float64(rate)
				gate := 1.0
				if math.Mod(t, 0.1) > 0.05 {
					gate = 0.6
				}
				v := (math.Sin(2*math.Pi*440*t) + math.Sin(2*math.Pi*480*t)) * 0.18 * gate
				set(at+i, v, v)
			}
		}
	case "sword":
		partials := []float64{1, 2.76, 5.4, 8.9}
		base := 1100.0
		for i := 0; i < n; i++ {
			t := float64(i) / float64(rate)
			var v float64
			for pi, p := range partials {
				v += math.Sin(2*math.Pi*base*p*t) * math.Exp(-t*(4+float64(pi)*3)) / float64(pi+1)
			}
			v = v*0.3 + noise()*math.Exp(-t*60)*0.4
			set(i, v, v)
		}
	case "explosion":
		lp := lowpassBQ(400, 0.7)
		for i := 0; i < n; i++ {
			t := float64(i) / float64(rate)
			v := lp.do(noise()) * math.Exp(-t*1.6) * 2.8
			if t < 0.01 {
				v *= t / 0.01
			}
			set(i, v, v)
		}
	case "hooves":
		for at, k := 0, 0; at < n; k++ {
			thump(at, 160, 60, 0.6)
			if k%2 == 0 {
				at += int(0.12 * float64(rate))
			} else {
				at += int(0.28 * float64(rate))
			}
		}
	case "crowd", "applause":
		bp := bandpass(1800, 0.5)
		for i := 0; i < n; i++ {
			t := float64(i) / float64(rate)
			v := bp.do(noise()) * (0.35 + 0.1*math.Sin(t*2.1))
			if kind == "crowd" {
				v = bp.do(brownNoise()) * (0.6 + 0.2*math.Sin(t*0.8))
			}
			set(i, v, v*0.92)
		}
		if kind == "crowd" {
			for k := 0; k < 4; k++ {
				b := Babble(strings.Repeat("嗯啊这那", 6), VoiceStyle{Gender: []string{"male", "female"}[k%2], Seed: seed + int64(k), Volume: -16})
				off := rng.Intn(n / 3)
				for i, v := range b.Data {
					if off+i < n {
						set(off+i, float64(v)*0.25, float64(v)*0.18)
					}
				}
			}
		}
	default: // whoosh
		bp := bandpass(600, 1.2)
		for i := 0; i < n; i++ {
			t := float64(i) / float64(n)
			if i%256 == 0 {
				*bp = *bandpass(300+3000*math.Sin(math.Pi*t), 1.5)
			}
			v := bp.do(noise()) * math.Sin(math.Pi*t) * 0.9
			set(i, v*(1-t), v*t)
		}
	}
	p := &PCM{Rate: rate, Channels: 2, Data: out}
	NormalizeRMS(p, -20, -1)
	fadeEdges(p, 15, 120)
	return p, kind
}

func fadeEdges(p *PCM, inMs, outMs int) {
	frames := p.Frames()
	fi := p.Rate * inMs / 1000
	fo := p.Rate * outMs / 1000
	for i := 0; i < frames; i++ {
		g := float32(1)
		if i < fi {
			g = float32(i) / float32(fi)
		}
		if frames-i < fo {
			g *= float32(frames-i) / float32(fo)
		}
		if g < 1 {
			for c := 0; c < p.Channels; c++ {
				p.Data[i*p.Channels+c] *= g
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Procedural music (placeholder generator for AI music / BGM)
// ---------------------------------------------------------------------------

type musicMood struct {
	key    string
	words  []string
	tempo  float64
	chords [][]int // semitone offsets from root
	minor  bool
	arp    bool
	drums  bool
	drone  bool
}

var moods = []musicMood{
	{"suspense", []string{"悬疑", "紧张", "恐怖", "惊悚", "suspense", "horror", "tense", "dark"}, 84, [][]int{{0, 3, 7}, {1, 4, 8}, {0, 3, 6}, {-1, 3, 6}}, true, false, false, true},
	{"epic", []string{"史诗", "战斗", "激昂", "热血", "epic", "battle", "heroic"}, 96, [][]int{{0, 3, 7}, {-4, 0, 3}, {3, 7, 10}, {-2, 2, 5}}, true, true, true, false},
	{"sad", []string{"悲伤", "伤感", "哀", "离别", "sad", "melancholy"}, 64, [][]int{{0, 3, 7}, {-4, 0, 3}, {3, 7, 10}, {-2, 2, 5}}, true, true, false, false},
	{"happy", []string{"欢快", "轻快", "日常", "活泼", "温馨", "happy", "cheerful", "bright"}, 108, [][]int{{0, 4, 7}, {7, 11, 14}, {9, 12, 16}, {5, 9, 12}}, false, true, false, false},
	{"mystic", []string{"神秘", "古风", "仙侠", "玄幻", "mystic", "fantasy", "oriental"}, 76, [][]int{{0, 2, 7}, {-3, 2, 7}, {-5, 0, 4}, {-2, 2, 7}}, false, true, false, true},
	{"calm", []string{"温柔", "舒缓", "宁静", "浪漫", "calm", "gentle", "romantic", "soft"}, 72, [][]int{{0, 4, 7, 11}, {-3, 0, 4, 7}, {-7, -3, 0, 4}, {-5, -1, 2, 5}}, false, true, false, false},
}

func DetectMood(prompt string) string {
	p := strings.ToLower(prompt)
	for _, m := range moods {
		for _, w := range m.words {
			if strings.Contains(p, w) {
				return m.key
			}
		}
	}
	return "calm"
}

func midiHz(n float64) float64 { return 440 * math.Pow(2, (n-69)/12) }

// GenerateMusic renders a loopable stereo cue for the mood detected in prompt.
func GenerateMusic(prompt string, durationMs int, seed int64) (*PCM, string) {
	key := DetectMood(prompt)
	var m musicMood
	for _, mm := range moods {
		if mm.key == key {
			m = mm
		}
	}
	rng := rand.New(rand.NewSource(seed))
	rate := SampleRate
	if durationMs <= 0 {
		durationMs = 60000
	}
	beat := 60 / m.tempo
	bar := beat * 4
	bars := int(math.Ceil(float64(durationMs) / 1000 / bar))
	if bars < 2 {
		bars = 2
	}
	n := int(float64(bars) * bar * float64(rate))
	L := make([]float64, n)
	R := make([]float64, n)
	root := 57.0 + float64(rng.Intn(5)) // A3..C#4
	add := func(i int, v, pan float64) {
		if i >= 0 && i < n {
			L[i] += v * (1 - pan) * 2 / 2
			R[i] += v * pan * 2 / 2
		}
	}
	for b := 0; b < bars; b++ {
		chord := m.chords[b%len(m.chords)]
		start := int(float64(b) * bar * float64(rate))
		length := int(bar * float64(rate))
		// pad
		for ci, iv := range chord {
			f := midiHz(root + float64(iv))
			det := []float64{-0.08, 0, 0.07}
			pan := 0.3 + 0.4*float64(ci)/float64(len(chord))
			ph := make([]float64, 3)
			lp := 0.0
			for i := 0; i < length; i++ {
				t := float64(i) / float64(rate)
				env := math.Min(1, t/0.9) * math.Min(1, (bar-t)/0.6)
				var s float64
				for d, dt := range det {
					ph[d] += 2 * math.Pi * f * math.Pow(2, dt/12) / float64(rate)
					s += math.Mod(ph[d], 2*math.Pi)/math.Pi - 1
				}
				lp += 0.04 * (s/3 - lp)
				add(start+i, lp*env*0.16, pan)
			}
		}
		// bass
		bf := midiHz(root - 24 + float64(chord[0]))
		for i := 0; i < length; i++ {
			t := float64(i) / float64(rate)
			env := math.Min(1, t/0.05) * math.Exp(-t*0.6)
			v := math.Tanh(math.Sin(2*math.Pi*bf*t)*1.4) * env * 0.22
			add(start+i, v, 0.5)
		}
		// arpeggio
		if m.arp {
			steps := 8
			if m.key == "sad" || m.key == "calm" {
				steps = 4
			}
			for s := 0; s < steps; s++ {
				iv := chord[(s+rng.Intn(2))%len(chord)] + 12*(s%2)
				f := midiHz(root + float64(iv))
				at := start + int(float64(s)*bar/float64(steps)*float64(rate))
				dur := int(0.9 * float64(rate))
				pan := 0.25 + 0.5*rng.Float64()
				for i := 0; i < dur; i++ {
					t := float64(i) / float64(rate)
					v := (math.Sin(2*math.Pi*f*t) + 0.3*math.Sin(4*math.Pi*f*t)) * math.Exp(-t*4.5) * 0.12
					add(at+i, v, pan)
				}
			}
		}
		// drums
		if m.drums {
			for bt := 0; bt < 4; bt++ {
				at := start + int(float64(bt)*beat*float64(rate))
				for i := 0; i < int(0.4*float64(rate)); i++ {
					t := float64(i) / float64(rate)
					f := 45 + 75*math.Exp(-t*28)
					v := math.Sin(2*math.Pi*f*t) * math.Exp(-t*8) * 0.55
					if bt%2 == 1 {
						v += (rng.Float64()*2 - 1) * math.Exp(-t*25) * 0.18
					}
					add(at+i, v, 0.5)
				}
			}
		}
		// suspense pulse / drone
		if m.drone {
			df := midiHz(root - 12)
			for i := 0; i < length; i++ {
				t := float64(start+i) / float64(rate)
				v := math.Sin(2*math.Pi*df*t) * 0.08 * (0.7 + 0.3*math.Sin(2*math.Pi*t/7))
				v += math.Sin(2*math.Pi*df*1.4983*t) * 0.03
				add(start+i, v, 0.5)
			}
			if m.key == "suspense" {
				for bt := 0; bt < 4; bt += 2 {
					at := start + int(float64(bt)*beat*float64(rate))
					for i := 0; i < int(0.3*float64(rate)); i++ {
						t := float64(i) / float64(rate)
						v := math.Sin(2*math.Pi*48*t) * math.Exp(-t*14) * 0.5
						add(at+i, v, 0.5)
					}
				}
			}
		}
	}
	// Schroeder-style reverb
	reverb := func(x []float64, delays []int) []float64 {
		y := make([]float64, len(x))
		for _, d := range delays {
			buf := make([]float64, d)
			idx := 0
			for i := range x {
				out := buf[idx]
				buf[idx] = x[i] + out*0.78
				idx = (idx + 1) % d
				y[i] += out * 0.2
			}
		}
		return y
	}
	rl := reverb(L, []int{1557, 1617, 1491, 1422})
	rr := reverb(R, []int{1277, 1356, 1188, 1116})
	data := make([]float32, n*2)
	for i := 0; i < n; i++ {
		data[i*2] = float32(L[i] + rl[i]*0.6)
		data[i*2+1] = float32(R[i] + rr[i]*0.6)
	}
	p := &PCM{Rate: rate, Channels: 2, Data: data}
	NormalizeRMS(p, -18, -1)
	fadeEdges(p, 30, 300)
	return p, key
}
