package audio

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestDecodeWAVStreamingPlaceholderSize(t *testing.T) {
	var buf bytes.Buffer
	if err := EncodeWAV16(&buf, &PCM{Rate: 24000, Channels: 1, Data: make([]float32, 2400)}); err != nil {
		t.Fatal(err)
	}
	b := buf.Bytes()
	// mimic a streaming encoder: placeholder sizes in the RIFF and data headers
	binary.LittleEndian.PutUint32(b[4:], 0x7fffffbf)
	binary.LittleEndian.PutUint32(b[40:], 0x7fffff9b)
	p, err := DecodeWAV(bytes.NewReader(b))
	if err != nil || len(p.Data) != 2400 {
		t.Fatalf("decode: %v %d", err, len(p.Data))
	}
	f := FixWAVSizes(append([]byte(nil), b...))
	if got := binary.LittleEndian.Uint32(f[40:]); got != 4800 {
		t.Fatalf("data size %d", got)
	}
	if got := binary.LittleEndian.Uint32(f[4:]); got != uint32(len(f)-8) {
		t.Fatalf("riff size %d", got)
	}
}
