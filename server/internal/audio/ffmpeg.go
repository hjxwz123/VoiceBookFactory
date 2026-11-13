package audio

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type FF struct {
	FFmpeg  string
	FFprobe string
}

func (f *FF) Available() bool {
	_, err := exec.LookPath(f.FFmpeg)
	return err == nil
}

func (f *FF) run(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, f.FFmpeg, append([]string{"-hide_banner", "-nostdin", "-y"}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := stderr.String()
		if len(msg) > 800 {
			msg = msg[len(msg)-800:]
		}
		return nil, fmt.Errorf("音频处理失败｜详情：ffmpeg：%v %s", err, strings.TrimSpace(msg)) // core.DetailSep
	}
	return append(out, stderr.Bytes()...), nil
}

type ProbeInfo struct {
	DurationMs int64
	SampleRate int
	Channels   int
	Codec      string
	Format     string
	BitRate    int
}

func (f *FF) Probe(ctx context.Context, path string) (*ProbeInfo, error) {
	cmd := exec.CommandContext(ctx, f.FFprobe, "-v", "error", "-print_format", "json", "-show_format", "-show_streams", path)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("无法识别的音频文件：%v", err)
	}
	var p struct {
		Streams []struct {
			CodecType  string `json:"codec_type"`
			CodecName  string `json:"codec_name"`
			SampleRate string `json:"sample_rate"`
			Channels   int    `json:"channels"`
			Duration   string `json:"duration"`
		} `json:"streams"`
		Format struct {
			Duration   string `json:"duration"`
			FormatName string `json:"format_name"`
			BitRate    string `json:"bit_rate"`
		} `json:"format"`
	}
	if err := json.Unmarshal(out, &p); err != nil {
		return nil, err
	}
	info := &ProbeInfo{Format: p.Format.FormatName}
	info.BitRate, _ = strconv.Atoi(p.Format.BitRate)
	dur, _ := strconv.ParseFloat(p.Format.Duration, 64)
	found := false
	for _, s := range p.Streams {
		if s.CodecType == "audio" {
			found = true
			info.Codec = s.CodecName
			info.SampleRate, _ = strconv.Atoi(s.SampleRate)
			info.Channels = s.Channels
			if dur == 0 {
				dur, _ = strconv.ParseFloat(s.Duration, 64)
			}
			break
		}
	}
	if !found {
		return nil, errors.New("文件中没有音频流")
	}
	info.DurationMs = int64(dur * 1000)
	return info, nil
}

// DecodePCM decodes any audio file into float32 PCM, optionally through an ffmpeg filter chain.
func (f *FF) DecodePCM(ctx context.Context, path string, rate, ch int, filter string) (*PCM, error) {
	args := []string{"-i", path}
	if filter != "" {
		args = append(args, "-af", filter)
	}
	args = append(args, "-ac", strconv.Itoa(ch), "-ar", strconv.Itoa(rate), "-f", "f32le", "-acodec", "pcm_f32le", "pipe:1")
	cmd := exec.CommandContext(ctx, f.FFmpeg, append([]string{"-hide_banner", "-nostdin", "-v", "error"}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("音频解码失败｜详情：%v %s", err, strings.TrimSpace(stderr.String())) // core.DetailSep
	}
	n := len(out) / 4
	p := &PCM{Rate: rate, Channels: ch, Data: make([]float32, n)}
	for i := 0; i < n; i++ {
		p.Data[i] = math.Float32frombits(binary.LittleEndian.Uint32(out[i*4:]))
	}
	return p, nil
}

// Tempo changes the speed of a recording without changing its pitch (factor 1.2 = 20% faster).
func (f *FF) Tempo(ctx context.Context, in, out string, factor float64) error {
	return f.ToWAV(ctx, in, out, SampleRate, 2, fmt.Sprintf("atempo=%.4f", factor))
}

// ToWAV converts to 16-bit PCM WAV at the given rate/channels with an optional filter.
func (f *FF) ToWAV(ctx context.Context, in, out string, rate, ch int, filter string) error {
	args := []string{"-i", in}
	if filter != "" {
		args = append(args, "-af", filter)
	}
	args = append(args, "-ac", strconv.Itoa(ch), "-ar", strconv.Itoa(rate), "-c:a", "pcm_s16le", out)
	_, err := f.run(ctx, args...)
	return err
}

// Slice cuts [startMs, endMs) into a WAV file.
func (f *FF) Slice(ctx context.Context, in, out string, startMs, endMs int64) error {
	_, err := f.run(ctx, "-ss", ms(startMs), "-to", ms(endMs), "-i", in, "-ac", "1", "-ar", strconv.Itoa(SampleRate), "-c:a", "pcm_s16le", out)
	return err
}

func ms(v int64) string { return strconv.FormatFloat(float64(v)/1000, 'f', 3, 64) }

type LoudnessStats struct {
	InputI      float64 `json:"inputI"`
	InputTP     float64 `json:"inputTp"`
	InputLRA    float64 `json:"inputLra"`
	InputThresh float64 `json:"inputThresh"`
	Offset      float64 `json:"offset"`
}

var loudJSON = regexp.MustCompile(`(?s)\{\s*"input_i".*?\}`)

// MeasureLoudness runs a loudnorm analysis pass.
func (f *FF) MeasureLoudness(ctx context.Context, in string, targetI, targetTP float64) (*LoudnessStats, error) {
	out, err := f.run(ctx, "-i", in, "-af", fmt.Sprintf("loudnorm=I=%.1f:TP=%.1f:LRA=11:print_format=json", targetI, targetTP), "-f", "null", "-")
	if err != nil {
		return nil, err
	}
	m := loudJSON.Find(out)
	if m == nil {
		return nil, errors.New("无法读取响度分析结果")
	}
	var raw map[string]string
	if err := json.Unmarshal(m, &raw); err != nil {
		return nil, err
	}
	pf := func(k string) float64 {
		v, _ := strconv.ParseFloat(raw[k], 64)
		if math.IsInf(v, 0) || math.IsNaN(v) {
			return -70
		}
		return v
	}
	return &LoudnessStats{InputI: pf("input_i"), InputTP: pf("input_tp"), InputLRA: pf("input_lra"), InputThresh: pf("input_thresh"), Offset: pf("target_offset")}, nil
}

type EncodeOptions struct {
	Format      string // mp3 | wav | m4a | m4b | flac
	Bitrate     int    // kbps
	SampleRate  int
	Channels    int
	TargetI     float64 // LUFS; 0 = no normalization
	TargetTP    float64
	Measured    *LoudnessStats
	Metadata    map[string]string
	CoverPath   string
	ChapterMeta string // ffmetadata file with chapters (m4b)
	ExtraFilter string
}

// Encode writes the final deliverable with two-pass loudness normalization and metadata.
func (f *FF) Encode(ctx context.Context, in, out string, o EncodeOptions) error {
	args := []string{"-i", in}
	inputs := 1
	if o.ChapterMeta != "" {
		args = append(args, "-i", o.ChapterMeta)
		inputs++
	}
	coverIdx := -1
	if o.CoverPath != "" && (o.Format == "mp3" || o.Format == "m4a" || o.Format == "m4b") {
		if _, err := os.Stat(o.CoverPath); err == nil {
			args = append(args, "-i", o.CoverPath)
			coverIdx = inputs
			inputs++
		}
	}
	var filters []string
	if o.ExtraFilter != "" {
		filters = append(filters, o.ExtraFilter)
	}
	if o.TargetI != 0 {
		tp := o.TargetTP
		if tp == 0 {
			tp = -1.5
		}
		if o.Measured != nil {
			filters = append(filters, fmt.Sprintf("loudnorm=I=%.1f:TP=%.1f:LRA=11:measured_I=%.2f:measured_TP=%.2f:measured_LRA=%.2f:measured_thresh=%.2f:offset=%.2f:linear=true",
				o.TargetI, tp, o.Measured.InputI, o.Measured.InputTP, o.Measured.InputLRA, o.Measured.InputThresh, o.Measured.Offset))
		} else {
			filters = append(filters, fmt.Sprintf("loudnorm=I=%.1f:TP=%.1f:LRA=11", o.TargetI, tp))
		}
	}
	args = append(args, "-map", "0:a")
	if coverIdx >= 0 {
		args = append(args, "-map", fmt.Sprintf("%d:v", coverIdx))
	}
	if o.ChapterMeta != "" {
		args = append(args, "-map_metadata", "1", "-map_chapters", "1")
	}
	if len(filters) > 0 {
		args = append(args, "-af", strings.Join(filters, ","))
	}
	rate := o.SampleRate
	if rate == 0 {
		rate = SampleRate
	}
	args = append(args, "-ar", strconv.Itoa(rate))
	if o.Channels > 0 {
		args = append(args, "-ac", strconv.Itoa(o.Channels))
	}
	br := o.Bitrate
	if br == 0 {
		br = 192
	}
	switch o.Format {
	case "mp3":
		args = append(args, "-c:a", "libmp3lame", "-b:a", fmt.Sprintf("%dk", br), "-id3v2_version", "3")
		if coverIdx >= 0 {
			args = append(args, "-c:v", "mjpeg", "-disposition:v", "attached_pic")
		}
	case "m4a", "m4b":
		args = append(args, "-c:a", "aac", "-b:a", fmt.Sprintf("%dk", br), "-movflags", "+faststart")
		if coverIdx >= 0 {
			args = append(args, "-c:v", "mjpeg", "-disposition:v", "attached_pic")
		}
		if o.Format == "m4b" {
			args = append(args, "-f", "ipod")
		}
	case "flac":
		args = append(args, "-c:a", "flac")
	default:
		args = append(args, "-c:a", "pcm_s16le")
	}
	for k, v := range o.Metadata {
		if v != "" {
			args = append(args, "-metadata", k+"="+v)
		}
	}
	args = append(args, out)
	_, err := f.run(ctx, args...)
	return err
}

// ConcatWAV concatenates WAV files with the concat demuxer.
func (f *FF) ConcatWAV(ctx context.Context, files []string, out string) error {
	list, err := os.CreateTemp("", "concat-*.txt")
	if err != nil {
		return err
	}
	defer os.Remove(list.Name())
	for _, p := range files {
		if abs, err := filepath.Abs(p); err == nil {
			p = abs // the concat demuxer resolves relative paths against the list file's folder (/tmp), not ours
		}
		fmt.Fprintf(list, "file '%s'\n", strings.ReplaceAll(p, "'", `'\''`))
	}
	list.Close()
	_, err = f.run(ctx, "-f", "concat", "-safe", "0", "-i", list.Name(), "-c:a", "pcm_s16le", out)
	return err
}

// EncodeChunk extracts [startMs,endMs) as a compact 16 kHz mono MP3 suitable for speech recognition.
func (f *FF) EncodeChunk(ctx context.Context, in, out string, startMs, endMs int64) (string, error) {
	_, err := f.run(ctx, "-ss", ms(startMs), "-to", ms(endMs), "-i", in, "-ac", "1", "-ar", "16000", "-c:a", "libmp3lame", "-b:a", "48k", out)
	return out, err
}

// ImageToJPEG converts any image (png/webp) for embedding as cover art.
func (f *FF) ImageToJPEG(ctx context.Context, in, out string) error {
	_, err := f.run(ctx, "-i", in, "-vf", "scale='min(1400,iw)':-2", "-frames:v", "1", "-q:v", "3", out)
	return err
}

// Cut writes [startMs, startMs+durMs) of `in` as 44.1 kHz 16-bit WAV through an optional filter chain,
// keeping mono sources mono and folding anything wider than stereo down to stereo. durMs <= 0 keeps the rest.
func (f *FF) Cut(ctx context.Context, in, out string, startMs, durMs int64, channels int, filter string) error {
	args := []string{}
	if startMs > 0 {
		args = append(args, "-ss", ms(startMs))
	}
	if durMs > 0 {
		args = append(args, "-t", ms(durMs))
	}
	args = append(args, "-i", in)
	if filter != "" {
		args = append(args, "-af", filter)
	}
	if channels < 1 || channels > 2 {
		channels = 2
	}
	args = append(args, "-ac", strconv.Itoa(channels), "-ar", "44100", "-c:a", "pcm_s16le", out)
	_, err := f.run(ctx, args...)
	return err
}

var maxVolRe = regexp.MustCompile(`max_volume:\s*(-?[\d.]+|-inf) dB`)

// MaxVolume returns the sample peak of a file in dBFS (-inf for silence is reported as -120).
func (f *FF) MaxVolume(ctx context.Context, in string) (float64, error) {
	out, err := f.run(ctx, "-i", in, "-af", "volumedetect", "-f", "null", "-")
	if err != nil {
		return 0, err
	}
	m := maxVolRe.FindSubmatch(out)
	if m == nil {
		return 0, errors.New("无法读取音量")
	}
	if string(m[1]) == "-inf" {
		return -120, nil
	}
	return strconv.ParseFloat(string(m[1]), 64)
}
