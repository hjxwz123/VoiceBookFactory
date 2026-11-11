package audio

import (
	"math"
	"sort"
)

type Stats struct {
	DurationMs     int64   `json:"durationMs"`
	PeakDB         float64 `json:"peakDb"`
	RMSDB          float64 `json:"rmsDb"`
	NoiseFloorDB   float64 `json:"noiseFloorDb"`
	HeadSilenceMs  int64   `json:"headSilenceMs"`
	TailSilenceMs  int64   `json:"tailSilenceMs"`
	ClippedSamples int     `json:"clippedSamples"`
	LongestGapMs   int64   `json:"longestGapMs"` // longest internal silence
	SpeechMs       int64   `json:"speechMs"`
}

func toDB(v float64) float64 {
	if v <= 1e-9 {
		return -120
	}
	return 20 * math.Log10(v)
}

// Analyze computes level, noise floor and silence statistics (mono-summed).
func Analyze(p *PCM) Stats {
	m := ToMono(p)
	s := Stats{DurationMs: m.DurationMs()}
	if len(m.Data) == 0 {
		s.PeakDB, s.RMSDB, s.NoiseFloorDB = -120, -120, -120
		return s
	}
	var peak, sum float64
	for _, v := range m.Data {
		a := math.Abs(float64(v))
		if a > peak {
			peak = a
		}
		if a >= 0.999 {
			s.ClippedSamples++
		}
		sum += float64(v) * float64(v)
	}
	s.PeakDB = toDB(peak)
	s.RMSDB = toDB(math.Sqrt(sum / float64(len(m.Data))))

	win := m.Rate / 20 // 50 ms
	if win == 0 {
		win = 1
	}
	var rms []float64
	for i := 0; i+win <= len(m.Data); i += win {
		var acc float64
		for _, v := range m.Data[i : i+win] {
			acc += float64(v) * float64(v)
		}
		rms = append(rms, toDB(math.Sqrt(acc/float64(win))))
	}
	if len(rms) == 0 {
		s.NoiseFloorDB = s.RMSDB
		return s
	}
	sorted := append([]float64(nil), rms...)
	sort.Float64s(sorted)
	s.NoiseFloorDB = sorted[len(sorted)/10]

	const silent = -50.0
	head := 0
	for head < len(rms) && rms[head] < silent {
		head++
	}
	tail := 0
	for tail < len(rms)-head && rms[len(rms)-1-tail] < silent {
		tail++
	}
	s.HeadSilenceMs = int64(head * 50)
	s.TailSilenceMs = int64(tail * 50)
	run, longest, speech := 0, 0, 0
	for i := head; i < len(rms)-tail; i++ {
		if rms[i] < silent {
			run++
			if run > longest {
				longest = run
			}
		} else {
			run = 0
			speech++
		}
	}
	s.LongestGapMs = int64(longest * 50)
	s.SpeechMs = int64(speech * 50)
	return s
}

// Peaks returns per-bucket absolute maxima (0..1) for waveform drawing.
func Peaks(p *PCM, perSecond int) []float32 {
	m := ToMono(p)
	if perSecond <= 0 {
		perSecond = 50
	}
	bucket := m.Rate / perSecond
	if bucket < 1 {
		bucket = 1
	}
	out := make([]float32, 0, len(m.Data)/bucket+1)
	for i := 0; i < len(m.Data); i += bucket {
		end := i + bucket
		if end > len(m.Data) {
			end = len(m.Data)
		}
		var mx float32
		for _, v := range m.Data[i:end] {
			if v < 0 {
				v = -v
			}
			if v > mx {
				mx = v
			}
		}
		out = append(out, float32(math.Round(float64(mx)*1000)/1000))
	}
	return out
}

// TrimSilence removes leading/trailing silence below thresholdDB, keeping padMs of room.
func TrimSilence(p *PCM, thresholdDB float64, padMs int) *PCM {
	th := float32(math.Pow(10, thresholdDB/20))
	frames := p.Frames()
	start, end := 0, frames
	loud := func(i int) bool {
		for c := 0; c < p.Channels; c++ {
			v := p.Data[i*p.Channels+c]
			if v > th || v < -th {
				return true
			}
		}
		return false
	}
	for start < frames && !loud(start) {
		start++
	}
	for end > start && !loud(end-1) {
		end--
	}
	if start >= end {
		return p
	}
	pad := p.Rate * padMs / 1000
	start -= pad
	if start < 0 {
		start = 0
	}
	end += pad
	if end > frames {
		end = frames
	}
	return &PCM{Rate: p.Rate, Channels: p.Channels, Data: append([]float32(nil), p.Data[start*p.Channels:end*p.Channels]...)}
}

// NormalizeRMS scales speech to a target RMS (dBFS) while keeping peaks below ceilingDB.
func NormalizeRMS(p *PCM, targetDB, ceilingDB float64) {
	st := Analyze(p)
	if st.RMSDB < -90 {
		return
	}
	gain := targetDB - st.RMSDB
	if st.PeakDB+gain > ceilingDB {
		gain = ceilingDB - st.PeakDB
	}
	g := DB(gain)
	for i := range p.Data {
		p.Data[i] *= g
	}
}
