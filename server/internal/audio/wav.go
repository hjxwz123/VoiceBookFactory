// Package audio contains PCM utilities, an ffmpeg wrapper, a streaming multi-track mixer,
// signal analysis, and procedural placeholder generators.
package audio

import (
	"bufio"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"os"
)

const SampleRate = 44100

// PCM is mono or interleaved float32 samples in [-1, 1].
type PCM struct {
	Rate     int
	Channels int
	Data     []float32
}

func (p *PCM) Frames() int {
	if p.Channels == 0 {
		return 0
	}
	return len(p.Data) / p.Channels
}

func (p *PCM) DurationMs() int64 {
	if p.Rate == 0 {
		return 0
	}
	return int64(p.Frames()) * 1000 / int64(p.Rate)
}

// EncodeWAV16 writes 16-bit PCM WAV.
func EncodeWAV16(w io.Writer, p *PCM) error {
	bw := bufio.NewWriter(w)
	dataLen := uint32(len(p.Data) * 2)
	writeHeader(bw, p.Rate, p.Channels, dataLen)
	buf := make([]byte, 2)
	for _, s := range p.Data {
		binary.LittleEndian.PutUint16(buf, uint16(toI16(s)))
		bw.Write(buf)
	}
	return bw.Flush()
}

func writeHeader(w io.Writer, rate, ch int, dataLen uint32) {
	h := make([]byte, 44)
	copy(h[0:], "RIFF")
	binary.LittleEndian.PutUint32(h[4:], 36+dataLen)
	copy(h[8:], "WAVE")
	copy(h[12:], "fmt ")
	binary.LittleEndian.PutUint32(h[16:], 16)
	binary.LittleEndian.PutUint16(h[20:], 1)
	binary.LittleEndian.PutUint16(h[22:], uint16(ch))
	binary.LittleEndian.PutUint32(h[24:], uint32(rate))
	binary.LittleEndian.PutUint32(h[28:], uint32(rate*ch*2))
	binary.LittleEndian.PutUint16(h[32:], uint16(ch*2))
	binary.LittleEndian.PutUint16(h[34:], 16)
	copy(h[36:], "data")
	binary.LittleEndian.PutUint32(h[40:], dataLen)
	w.Write(h)
}

func toI16(s float32) int16 {
	v := float64(s)
	if v > 1 {
		v = 1
	} else if v < -1 {
		v = -1
	}
	return int16(math.Round(v * 32767))
}

func WriteWAVFile(path string, p *PCM) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return EncodeWAV16(f, p)
}

// DecodeWAV reads PCM 16/24/32-bit integer or 32-bit float WAV.
func DecodeWAV(r io.Reader) (*PCM, error) {
	br := bufio.NewReader(r)
	hdr := make([]byte, 12)
	if _, err := io.ReadFull(br, hdr); err != nil {
		return nil, err
	}
	if string(hdr[0:4]) != "RIFF" || string(hdr[8:12]) != "WAVE" {
		return nil, errors.New("not a WAV file")
	}
	var (
		format, ch, bits uint16
		rate             uint32
	)
	for {
		ck := make([]byte, 8)
		if _, err := io.ReadFull(br, ck); err != nil {
			return nil, err
		}
		id := string(ck[0:4])
		size := binary.LittleEndian.Uint32(ck[4:])
		switch id {
		case "fmt ":
			if size < 16 || size > 1024 {
				return nil, errors.New("invalid WAV fmt chunk")
			}
			b := make([]byte, size)
			if _, err := io.ReadFull(br, b); err != nil {
				return nil, err
			}
			format = binary.LittleEndian.Uint16(b[0:])
			ch = binary.LittleEndian.Uint16(b[2:])
			rate = binary.LittleEndian.Uint32(b[4:])
			bits = binary.LittleEndian.Uint16(b[14:])
			if format == 0xFFFE && size >= 26 { // WAVE_FORMAT_EXTENSIBLE
				format = binary.LittleEndian.Uint16(b[24:])
			}
		case "data":
			if ch == 0 {
				return nil, errors.New("WAV missing fmt chunk")
			}
			// Streaming encoders write a placeholder size (0xFFFFFFFF / 0x7FFFFFxx) or 0;
			// read only what is actually present instead of trusting the header.
			var data []byte
			if size == 0 || size >= 0x7FFF0000 {
				data, _ = io.ReadAll(br)
			} else {
				data, _ = io.ReadAll(io.LimitReader(br, int64(size)))
			}
			p := &PCM{Rate: int(rate), Channels: int(ch)}
			switch {
			case format == 1 && bits == 16:
				p.Data = make([]float32, len(data)/2)
				for i := range p.Data {
					p.Data[i] = float32(int16(binary.LittleEndian.Uint16(data[i*2:]))) / 32768
				}
			case format == 1 && bits == 24:
				p.Data = make([]float32, len(data)/3)
				for i := range p.Data {
					v := int32(data[i*3]) | int32(data[i*3+1])<<8 | int32(int8(data[i*3+2]))<<16
					p.Data[i] = float32(v) / 8388608
				}
			case format == 1 && bits == 32:
				p.Data = make([]float32, len(data)/4)
				for i := range p.Data {
					p.Data[i] = float32(int32(binary.LittleEndian.Uint32(data[i*4:]))) / 2147483648
				}
			case format == 3 && bits == 32:
				p.Data = make([]float32, len(data)/4)
				for i := range p.Data {
					p.Data[i] = math.Float32frombits(binary.LittleEndian.Uint32(data[i*4:]))
				}
			default:
				return nil, errors.New("unsupported WAV encoding")
			}
			return p, nil
		default:
			if _, err := br.Discard(int(size + size%2)); err != nil {
				return nil, err
			}
		}
	}
}

func ReadWAVFile(path string) (*PCM, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return DecodeWAV(f)
}

// StreamWAV writes 16-bit stereo/mono PCM incrementally; Close patches the header sizes.
type StreamWAV struct {
	f     *os.File
	bw    *bufio.Writer
	rate  int
	ch    int
	bytes uint32
	buf   []byte
}

func CreateStreamWAV(path string, rate, ch int) (*StreamWAV, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	s := &StreamWAV{f: f, bw: bufio.NewWriterSize(f, 1<<16), rate: rate, ch: ch, buf: make([]byte, 2)}
	writeHeader(s.bw, rate, ch, 0)
	return s, nil
}

func (s *StreamWAV) Write(samples []float32) {
	for _, v := range samples {
		binary.LittleEndian.PutUint16(s.buf, uint16(toI16(v)))
		s.bw.Write(s.buf)
	}
	s.bytes += uint32(len(samples) * 2)
}

func (s *StreamWAV) Close() error {
	if err := s.bw.Flush(); err != nil {
		s.f.Close()
		return err
	}
	b := make([]byte, 4)
	binary.LittleEndian.PutUint32(b, 36+s.bytes)
	s.f.WriteAt(b, 4)
	binary.LittleEndian.PutUint32(b, s.bytes)
	s.f.WriteAt(b, 40)
	return s.f.Close()
}

// ToMono averages channels.
func ToMono(p *PCM) *PCM {
	if p.Channels == 1 {
		return p
	}
	n := p.Frames()
	out := &PCM{Rate: p.Rate, Channels: 1, Data: make([]float32, n)}
	for i := 0; i < n; i++ {
		var sum float32
		for c := 0; c < p.Channels; c++ {
			sum += p.Data[i*p.Channels+c]
		}
		out.Data[i] = sum / float32(p.Channels)
	}
	return out
}

// Resample converts sample rate with linear interpolation (adequate for placeholder material).
func Resample(p *PCM, rate int) *PCM {
	if p.Rate == rate || p.Rate == 0 {
		return p
	}
	frames := p.Frames()
	outFrames := int(int64(frames) * int64(rate) / int64(p.Rate))
	out := &PCM{Rate: rate, Channels: p.Channels, Data: make([]float32, outFrames*p.Channels)}
	ratio := float64(p.Rate) / float64(rate)
	for i := 0; i < outFrames; i++ {
		pos := float64(i) * ratio
		i0 := int(pos)
		frac := float32(pos - float64(i0))
		i1 := i0 + 1
		if i1 >= frames {
			i1 = frames - 1
		}
		for c := 0; c < p.Channels; c++ {
			a := p.Data[i0*p.Channels+c]
			b := p.Data[i1*p.Channels+c]
			out.Data[i*p.Channels+c] = a + (b-a)*frac
		}
	}
	return out
}

func DB(db float64) float32 { return float32(math.Pow(10, db/20)) }

// FixWAVSizes rewrites the RIFF and data chunk sizes of an in-memory WAV to match its
// real length. Streaming TTS services emit placeholder sizes, which some players and
// decoders mishandle. Non-WAV input is returned unchanged.
func FixWAVSizes(b []byte) []byte {
	if len(b) < 12 || string(b[0:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		return b
	}
	binary.LittleEndian.PutUint32(b[4:], uint32(len(b)-8))
	for i := 12; i+8 <= len(b); {
		size := int(binary.LittleEndian.Uint32(b[i+4:]))
		if string(b[i:i+4]) == "data" {
			if size == 0 || i+8+size > len(b) {
				binary.LittleEndian.PutUint32(b[i+4:], uint32(len(b)-i-8))
			}
			break
		}
		i += 8 + size + size%2
	}
	return b
}
