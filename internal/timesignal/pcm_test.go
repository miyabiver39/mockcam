package timesignal

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"
)

// buildWAV creates a RIFF/WAVE byte slice for tests. extraChunks are
// inserted between fmt and data to simulate LIST/fact chunks.
func buildWAV(sampleRate, channels, bits int, frames [][]int, extraChunks ...[]byte) []byte {
	var body bytes.Buffer
	for _, frame := range frames {
		for c := 0; c < channels; c++ {
			v := frame[c]
			if bits == 16 {
				_ = binary.Write(&body, binary.LittleEndian, int16(v))
			} else {
				body.WriteByte(byte(v/256 + 128))
			}
		}
	}

	var fmtChunk bytes.Buffer
	_ = binary.Write(&fmtChunk, binary.LittleEndian, uint16(1))
	_ = binary.Write(&fmtChunk, binary.LittleEndian, uint16(channels))
	_ = binary.Write(&fmtChunk, binary.LittleEndian, uint32(sampleRate))
	_ = binary.Write(&fmtChunk, binary.LittleEndian, uint32(sampleRate*channels*bits/8))
	_ = binary.Write(&fmtChunk, binary.LittleEndian, uint16(channels*bits/8))
	_ = binary.Write(&fmtChunk, binary.LittleEndian, uint16(bits))

	var out bytes.Buffer
	out.WriteString("RIFF")
	_ = binary.Write(&out, binary.LittleEndian, uint32(0)) // patched below
	out.WriteString("WAVE")
	out.WriteString("fmt ")
	_ = binary.Write(&out, binary.LittleEndian, uint32(fmtChunk.Len()))
	out.Write(fmtChunk.Bytes())
	for _, c := range extraChunks {
		out.Write(c)
	}
	out.WriteString("data")
	_ = binary.Write(&out, binary.LittleEndian, uint32(body.Len()))
	out.Write(body.Bytes())

	b := out.Bytes()
	binary.LittleEndian.PutUint32(b[4:], uint32(len(b)-8))
	return b
}

func chunk(id string, payload []byte) []byte {
	var b bytes.Buffer
	b.WriteString(id)
	_ = binary.Write(&b, binary.LittleEndian, uint32(len(payload)))
	b.Write(payload)
	if len(payload)%2 == 1 {
		b.WriteByte(0)
	}
	return b.Bytes()
}

func TestParseWAVMono16(t *testing.T) {
	wav := buildWAV(22050, 1, 16, [][]int{{100}, {-200}, {300}})
	pcm, err := ParseWAV(wav)
	if err != nil {
		t.Fatal(err)
	}
	if pcm.SampleRate != 22050 {
		t.Fatalf("rate = %d", pcm.SampleRate)
	}
	want := []int16{100, -200, 300}
	for i, v := range want {
		if pcm.Samples[i] != v {
			t.Fatalf("sample %d = %d, want %d", i, pcm.Samples[i], v)
		}
	}
}

func TestParseWAVStereoDownmixAndExtraChunks(t *testing.T) {
	list := chunk("LIST", []byte("INFOISFT\x05\x00\x00\x00test\x00"))
	fact := chunk("fact", []byte{2, 0, 0, 0})
	wav := buildWAV(48000, 2, 16, [][]int{{1000, 3000}, {-1000, 1000}}, list, fact)
	pcm, err := ParseWAV(wav)
	if err != nil {
		t.Fatal(err)
	}
	if pcm.SampleRate != 48000 || len(pcm.Samples) != 2 {
		t.Fatalf("rate=%d len=%d", pcm.SampleRate, len(pcm.Samples))
	}
	if pcm.Samples[0] != 2000 || pcm.Samples[1] != 0 {
		t.Fatalf("downmix = %v", pcm.Samples)
	}
}

func TestParseWAV8Bit(t *testing.T) {
	wav := buildWAV(8000, 1, 8, [][]int{{0}, {16384}, {-16384}})
	pcm, err := ParseWAV(wav)
	if err != nil {
		t.Fatal(err)
	}
	if pcm.Samples[0] != 0 || pcm.Samples[1] != 16384 || pcm.Samples[2] != -16384 {
		t.Fatalf("8-bit decode = %v", pcm.Samples)
	}
}

func TestParseWAVStreamingDataSize(t *testing.T) {
	// espeak --stdout writes a data chunk with size 0xFFFFFFFF.
	wav := buildWAV(22050, 1, 16, [][]int{{1}, {2}, {3}, {4}})
	dataPos := bytes.Index(wav, []byte("data"))
	binary.LittleEndian.PutUint32(wav[dataPos+4:], 0xFFFFFFFF)
	pcm, err := ParseWAV(wav)
	if err != nil {
		t.Fatal(err)
	}
	if len(pcm.Samples) != 4 {
		t.Fatalf("len = %d, want 4", len(pcm.Samples))
	}

	binary.LittleEndian.PutUint32(wav[dataPos+4:], 0)
	pcm, err = ParseWAV(wav)
	if err != nil || len(pcm.Samples) != 4 {
		t.Fatalf("size 0: err=%v len=%d", err, len(pcm.Samples))
	}
}

func TestParseWAVErrors(t *testing.T) {
	if _, err := ParseWAV([]byte("not a wav")); err == nil {
		t.Fatal("garbage should fail")
	}
	if _, err := ParseWAV([]byte("RIFF\x00\x00\x00\x00WAVE")); err == nil {
		t.Fatal("missing chunks should fail")
	}
	// 24-bit is unsupported.
	wav := buildWAV(44100, 1, 16, [][]int{{1}})
	fmtPos := bytes.Index(wav, []byte("fmt "))
	binary.LittleEndian.PutUint16(wav[fmtPos+8+14:], 24)
	if _, err := ParseWAV(wav); err == nil {
		t.Fatal("24-bit should be rejected")
	}
}

func TestResampleUpDown(t *testing.T) {
	// 1 kHz sine at 22050 Hz, upsampled to 48000 Hz: length scales by the ratio
	// and the signal stays a 1 kHz sine.
	const inRate = 22050
	in := make([]int16, inRate) // 1 second
	for i := range in {
		in[i] = int16(10000 * math.Sin(2*math.Pi*1000*float64(i)/inRate))
	}
	up := Resample(PCM{SampleRate: inRate, Samples: in}, 48000)
	if up.SampleRate != 48000 || math.Abs(float64(len(up.Samples))-48000) > 1 {
		t.Fatalf("upsampled rate=%d len=%d", up.SampleRate, len(up.Samples))
	}
	if r := rms(up.Samples); math.Abs(r-10000.0/32767.0/math.Sqrt2) > 0.01 {
		t.Fatalf("upsampled rms = %v", r)
	}

	down := Resample(up, 16000)
	if down.SampleRate != 16000 || math.Abs(float64(len(down.Samples))-16000) > 1 {
		t.Fatalf("downsampled rate=%d len=%d", down.SampleRate, len(down.Samples))
	}
	// Box averaging over 3 samples attenuates a 1 kHz tone at 48 kHz only slightly.
	if r := rms(down.Samples); r < 0.19 || r > 0.22 {
		t.Fatalf("downsampled rms = %v", r)
	}

	// Same-rate and empty inputs pass through unchanged.
	same := Resample(PCM{SampleRate: 48000, Samples: in}, 48000)
	if len(same.Samples) != len(in) {
		t.Fatal("same-rate resample should not change length")
	}
	empty := Resample(PCM{SampleRate: 48000}, 16000)
	if len(empty.Samples) != 0 || empty.SampleRate != 16000 {
		t.Fatal("empty input should produce empty output at target rate")
	}
}

func TestTrimSilence(t *testing.T) {
	samples := make([]int16, 3000)
	for i := 1000; i < 2000; i++ {
		samples[i] = 5000
	}
	trimmed := TrimSilence(samples, 250, 48000)
	if len(trimmed) != 1000 {
		t.Fatalf("trimmed len = %d, want 1000", len(trimmed))
	}
	// Fades: first and last samples are attenuated to zero, the middle is untouched.
	if trimmed[0] != 0 || trimmed[len(trimmed)-1] != 0 {
		t.Fatalf("fade edges = %d,%d", trimmed[0], trimmed[len(trimmed)-1])
	}
	if trimmed[500] != 5000 {
		t.Fatalf("middle = %d", trimmed[500])
	}
	// The input slice must not be modified.
	if samples[1000] != 5000 {
		t.Fatal("TrimSilence must not mutate its input")
	}

	if got := TrimSilence(make([]int16, 100), 250, 48000); len(got) != 0 {
		t.Fatalf("pure silence should trim to empty, got %d", len(got))
	}
	if got := TrimSilence(nil, 250, 48000); len(got) != 0 {
		t.Fatal("nil input")
	}
}

func TestNormalize(t *testing.T) {
	samples := []int16{100, -3276, 50}
	out := Normalize(samples, 0.5)
	if out[1] != -16383 && out[1] != -16384 {
		t.Fatalf("peak after normalize = %d, want ~-16383", out[1])
	}
	if samples[1] != -3276 {
		t.Fatal("Normalize must not mutate its input")
	}
	if got := Normalize([]int16{0, 0}, 0.5); got[0] != 0 {
		t.Fatal("silence stays silent")
	}
}

func TestEncodeLE16(t *testing.T) {
	out := encodeLE16([]int16{1, -1, 256}, nil)
	want := []byte{1, 0, 0xFF, 0xFF, 0, 1}
	if !bytes.Equal(out, want) {
		t.Fatalf("encoded = %v, want %v", out, want)
	}
	// Reuses the provided buffer when large enough.
	buf := make([]byte, 10)
	out2 := encodeLE16([]int16{7}, buf)
	if &out2[0] != &buf[0] || len(out2) != 2 {
		t.Fatal("buffer should be reused")
	}
}
