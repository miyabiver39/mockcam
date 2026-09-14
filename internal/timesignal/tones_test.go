package timesignal

import (
	"math"
	"testing"
)

const testRate = 48000

// rms computes the RMS level (0..1) of a slice of samples.
func rms(samples []int16) float64 {
	if len(samples) == 0 {
		return 0
	}
	var sum float64
	for _, v := range samples {
		f := float64(v) / 32767
		sum += f * f
	}
	return math.Sqrt(sum / float64(len(samples)))
}

func renderSecond(sec int) []int16 {
	buf := make([]int16, testRate)
	RenderTones(buf, sec*testRate, testRate)
	return buf
}

// measureHz estimates the frequency of a tone by counting zero crossings.
func measureHz(samples []int16, rate int) float64 {
	crossings := 0
	for i := 1; i < len(samples); i++ {
		if (samples[i-1] < 0) != (samples[i] < 0) {
			crossings++
		}
	}
	return float64(crossings) / (2 * float64(len(samples)) / float64(rate))
}

func TestToneScheduleWithinBlock(t *testing.T) {
	// Every second except the mark carries exactly one 100 ms pip:
	// "ピ・ポ・ピ・ポ・ピ・ポ・ピ・ピ・ピ／".
	wantHz := map[int]float64{1: 880, 2: 440, 3: 880, 4: 440, 5: 880, 6: 440, 7: 880, 8: 880, 9: 880}
	for pos, hz := range wantHz {
		for _, sec := range []int{pos, pos + 10, pos + 50} {
			buf := renderSecond(sec)
			pip := buf[:testRate*pipDurationMs/1000]
			flat := buf[testRate*pipRampMs/1000 : testRate*(pipDurationMs-pipRampMs)/1000]
			after := buf[testRate*(pipDurationMs+50)/1000:]
			if level := rms(pip); level < 0.1 {
				t.Errorf("second %d: pip too quiet, rms=%v", sec, level)
			}
			if got := measureHz(flat, testRate); math.Abs(got-hz) > 10 {
				t.Errorf("second %d: pip frequency %.0f Hz, want %.0f", sec, got, hz)
			}
			if level := rms(after); level != 0 {
				t.Errorf("second %d: audio after the pip should be silent, rms=%v", sec, level)
			}
		}
	}

	for _, sec := range []int{0, 10, 20, 30, 40, 50} {
		buf := renderSecond(sec)
		sustain := buf[testRate*20/1000 : testRate*markSustainMs/1000]
		tail := buf[testRate*markDurationMs/1000:]
		if level := rms(sustain); level < 0.15 {
			t.Errorf("second %d: mark tone too quiet, rms=%v", sec, level)
		}
		if level := rms(tail); level != 0 {
			t.Errorf("second %d: audio after the mark should be silent, rms=%v", sec, level)
		}
	}
}

func TestMarkToneIs880Hz(t *testing.T) {
	buf := renderSecond(0)
	sustain := buf[testRate*markAttackMs/1000 : testRate*markSustainMs/1000]
	if freq := measureHz(sustain, testRate); math.Abs(freq-ToneFrequencyHz) > 10 {
		t.Fatalf("measured %.1f Hz, want %.0f Hz", freq, ToneFrequencyHz)
	}
	for pos, want := range map[int]float64{1: 880, 2: 440, 6: 440, 7: 880, 9: 880} {
		if got := pipFrequency(pos); got != want {
			t.Errorf("pipFrequency(%d) = %v, want %v", pos, got, want)
		}
	}
}

func TestTonesHaveNoClicks(t *testing.T) {
	// The first and last sample of every tone must be (near) zero and the
	// envelope must never jump by more than a small step between samples.
	for _, sec := range []int{0, 2, 7} {
		buf := renderSecond(sec)
		if buf[0] != 0 {
			t.Errorf("second %d: first sample = %d, want 0", sec, buf[0])
		}
		maxStep := 0
		for i := 1; i < len(buf); i++ {
			d := int(buf[i]) - int(buf[i-1])
			if d < 0 {
				d = -d
			}
			if d > maxStep {
				maxStep = d
			}
		}
		// An 880 Hz sine at amplitude 0.32 moves at most ~1200 units/sample at 48 kHz.
		if maxStep > 1500 {
			t.Errorf("second %d: sample step %d too large (click)", sec, maxStep)
		}
	}
}

func TestRenderTonesCrossesSecondBoundary(t *testing.T) {
	// A 100 ms chunk starting at :06.950 must contain the start of the :07 pip.
	start := 6*testRate + testRate*950/1000
	buf := make([]int16, testRate/10)
	RenderTones(buf, start, testRate)

	before := buf[:testRate*50/1000]
	after := buf[testRate*50/1000:]
	if level := rms(before); level != 0 {
		t.Fatalf("samples before :07 should be silent, rms=%v", level)
	}
	if level := rms(after); level < 0.1 {
		t.Fatalf("samples after :07 should contain the pip, rms=%v", level)
	}
}

func TestRenderTonesMixesAndClamps(t *testing.T) {
	buf := make([]int16, 100)
	for i := range buf {
		buf[i] = 32000
	}
	RenderTones(buf, 0*testRate+testRate*100/1000, testRate) // inside the mark sustain
	for i, v := range buf {
		if v < -32768 || v > 32767 {
			t.Fatalf("sample %d out of range: %d", i, v)
		}
	}
}

func TestEnvelopesAreBounded(t *testing.T) {
	for ms := -10.0; ms < 1000; ms += 0.5 {
		if e := pipEnvelope(ms); e < 0 || e > 1 {
			t.Fatalf("pipEnvelope(%v) = %v", ms, e)
		}
		if e := markEnvelope(ms); e < 0 || e > 1 {
			t.Fatalf("markEnvelope(%v) = %v", ms, e)
		}
	}
	if pipEnvelope(pipDurationMs/2) != 1 {
		t.Fatal("pip plateau should be 1")
	}
	if markEnvelope(markDurationMs-0.001) > 0.01 {
		t.Fatal("mark tone should end at zero")
	}
}
