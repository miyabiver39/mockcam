package timesignal

import (
	"encoding/binary"
	"errors"
	"math"
)

// PCM is a mono 16-bit signed audio buffer at a known sample rate.
type PCM struct {
	SampleRate int
	Samples    []int16
}

var (
	errNotWAV      = errors.New("not a RIFF/WAVE file")
	errNoFmtChunk  = errors.New("wav: missing fmt chunk")
	errNoDataChunk = errors.New("wav: missing data chunk")
	errUnsupported = errors.New("wav: unsupported format (only PCM 8/16-bit is accepted)")
)

// ParseWAV decodes a RIFF/WAVE file (PCM, 8 or 16 bit, any channel count)
// into mono 16-bit samples. Chunks are walked properly so that files with
// LIST/INFO or fact chunks (as produced by Open JTalk, espeak or SAPI) work.
func ParseWAV(data []byte) (PCM, error) {
	if len(data) < 12 || string(data[0:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return PCM{}, errNotWAV
	}

	var (
		format, channels, bits int
		sampleRate             int
		haveFmt                bool
		pcmBytes               []byte
	)

	pos := 12
	for pos+8 <= len(data) {
		id := string(data[pos : pos+4])
		size := int(binary.LittleEndian.Uint32(data[pos+4 : pos+8]))
		body := pos + 8
		end := body + size
		if end > len(data) {
			end = len(data) // tolerate truncated/streaming writers
		}

		switch id {
		case "fmt ":
			if end-body < 16 {
				return PCM{}, errNoFmtChunk
			}
			format = int(binary.LittleEndian.Uint16(data[body : body+2]))
			channels = int(binary.LittleEndian.Uint16(data[body+2 : body+4]))
			sampleRate = int(binary.LittleEndian.Uint32(data[body+4 : body+8]))
			bits = int(binary.LittleEndian.Uint16(data[body+14 : body+16]))
			haveFmt = true
		case "data":
			// Streaming writers (espeak --stdout, ffmpeg pipes) emit a
			// placeholder size of 0 or 0xFFFFFFFF; treat it as "until EOF".
			if size == 0 || size == 0xFFFFFFFF {
				end = len(data)
				size = end - body
			}
			pcmBytes = data[body:end]
		}

		pos = body + size
		if size%2 == 1 {
			pos++ // chunks are word aligned
		}
	}

	if !haveFmt {
		return PCM{}, errNoFmtChunk
	}
	if pcmBytes == nil {
		return PCM{}, errNoDataChunk
	}
	const wavePCM = 1
	const waveExtensible = 0xFFFE
	if (format != wavePCM && format != waveExtensible) || channels <= 0 || sampleRate <= 0 || (bits != 16 && bits != 8) {
		return PCM{}, errUnsupported
	}

	bytesPerSample := bits / 8
	frameSize := bytesPerSample * channels
	frames := len(pcmBytes) / frameSize
	out := make([]int16, frames)

	for f := 0; f < frames; f++ {
		var sum int
		for c := 0; c < channels; c++ {
			off := f*frameSize + c*bytesPerSample
			if bits == 16 {
				sum += int(int16(binary.LittleEndian.Uint16(pcmBytes[off:])))
			} else {
				sum += (int(pcmBytes[off]) - 128) << 8
			}
		}
		out[f] = int16(sum / channels)
	}

	return PCM{SampleRate: sampleRate, Samples: out}, nil
}

// Resample converts p to the target sample rate. Upsampling uses linear
// interpolation; downsampling averages the covered input window first so
// that high-frequency content does not alias. Speech quality is more than
// adequate for an announcement voice.
func Resample(p PCM, targetRate int) PCM {
	if targetRate <= 0 || p.SampleRate <= 0 || p.SampleRate == targetRate || len(p.Samples) == 0 {
		return PCM{SampleRate: targetRate, Samples: p.Samples}
	}

	ratio := float64(p.SampleRate) / float64(targetRate)
	outLen := int(math.Round(float64(len(p.Samples)) / ratio))
	out := make([]int16, outLen)
	in := p.Samples

	if ratio < 1 {
		// Upsampling: linear interpolation.
		for i := range out {
			pos := float64(i) * ratio
			idx := int(pos)
			frac := pos - float64(idx)
			a := float64(in[idx])
			b := a
			if idx+1 < len(in) {
				b = float64(in[idx+1])
			}
			out[i] = clampSample(a + (b-a)*frac)
		}
		return PCM{SampleRate: targetRate, Samples: out}
	}

	// Downsampling: box average over the input window covered by each output sample.
	window := int(math.Ceil(ratio))
	for i := range out {
		start := int(float64(i) * ratio)
		end := start + window
		if end > len(in) {
			end = len(in)
		}
		if start >= end {
			out[i] = in[len(in)-1]
			continue
		}
		var sum int
		for j := start; j < end; j++ {
			sum += int(in[j])
		}
		out[i] = int16(sum / (end - start))
	}
	return PCM{SampleRate: targetRate, Samples: out}
}

// TrimSilence removes leading/trailing samples whose magnitude never exceeds
// threshold, then applies a short raised-cosine fade at both ends.
func TrimSilence(samples []int16, threshold int16, sampleRate int) []int16 {
	if len(samples) == 0 {
		return samples
	}
	abs := func(v int16) int16 {
		if v < 0 {
			return -v
		}
		return v
	}

	start := 0
	for start < len(samples) && abs(samples[start]) <= threshold {
		start++
	}
	if start == len(samples) {
		return samples[:0] // pure silence
	}
	end := len(samples) - 1
	for end > start && abs(samples[end]) <= threshold {
		end--
	}

	trimmed := make([]int16, end-start+1)
	copy(trimmed, samples[start:end+1])

	fadeLen := sampleRate * 10 / 1000
	if fadeLen*2 < len(trimmed) {
		for i := 0; i < fadeLen; i++ {
			g := raisedCosineRamp(float64(i) / float64(fadeLen))
			trimmed[i] = int16(float64(trimmed[i]) * g)
			j := len(trimmed) - 1 - i
			trimmed[j] = int16(float64(trimmed[j]) * g)
		}
	}
	return trimmed
}

// Normalize scales samples so that the peak magnitude equals targetPeak
// (0 < targetPeak <= 1). Silent input is returned unchanged.
func Normalize(samples []int16, targetPeak float64) []int16 {
	var peak int16
	for _, v := range samples {
		if v < 0 {
			v = -v
		}
		if v > peak {
			peak = v
		}
	}
	if peak == 0 || targetPeak <= 0 {
		return samples
	}
	gain := targetPeak * 32767 / float64(peak)
	out := make([]int16, len(samples))
	for i, v := range samples {
		out[i] = clampSample(float64(v) * gain)
	}
	return out
}

// encodeLE16 converts samples into little-endian bytes suitable for
// FFmpeg's "-f s16le" demuxer.
func encodeLE16(samples []int16, dst []byte) []byte {
	if cap(dst) < len(samples)*2 {
		dst = make([]byte, len(samples)*2)
	}
	dst = dst[:len(samples)*2]
	for i, v := range samples {
		binary.LittleEndian.PutUint16(dst[i*2:], uint16(v))
	}
	return dst
}
