package timesignal

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	SampleRate = 22050
	ChunkMS    = 100 // 100ms per audio packet
)

// Service provides streaming 117 speaking clock audio.
type Service struct {
	mu            sync.Mutex
	voiceCache    map[string][]byte
	hasOpenJTalk  bool
	hasEspeak     bool
	hasPowerShell bool
}

// NewService initializes the time signal audio service.
func NewService() *Service {
	s := &Service{
		voiceCache: make(map[string][]byte),
	}

	// Check for open_jtalk availability
	if _, err := exec.LookPath("open_jtalk"); err == nil {
		s.hasOpenJTalk = true
	}

	// Check for espeak-ng availability
	if _, err := exec.LookPath("espeak-ng"); err == nil {
		s.hasEspeak = true
	} else if _, err := exec.LookPath("espeak"); err == nil {
		s.hasEspeak = true
	}

	// Check for powershell availability on Windows
	if runtime.GOOS == "windows" {
		if _, err := exec.LookPath("powershell"); err == nil {
			s.hasPowerShell = true
		}
	}

	return s
}

// HandleAudioStream handles HTTP GET /api/audio/timesignal?lang=ja (or en)
// It outputs continuous 16-bit 22050Hz mono PCM audio.
func (s *Service) HandleAudioStream(w http.ResponseWriter, r *http.Request) {
	lang := strings.ToLower(r.URL.Query().Get("lang"))
	if lang != "en" {
		lang = "ja"
	}

	w.Header().Set("Content-Type", "audio/l16; rate=22050; channels=1")
	w.Header().Set("Cache-Control", "no-cache, no-store")
	w.Header().Set("Connection", "close")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	ctx := r.Context()
	ticker := time.NewTicker(time.Duration(ChunkMS) * time.Millisecond)
	defer ticker.Stop()

	chunkSamples := SampleRate * ChunkMS / 1000
	pcmBuf := make([]int16, chunkSamples)
	byteBuf := make([]byte, chunkSamples*2)

	var lastAnnouncedMinSec string
	var currentPhrasePCM []int16
	var phraseOffset int

	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			sec := now.Second()
			nano := now.Nanosecond()
			msIntoSec := nano / 1_000_000

			// Determine if we need to start a time announcement
			// Announce every 10 seconds: start at :01, :11, :21, :31, :41, :51
			// so that speech finishes cleanly around :05 before the :07 preview chime.
			currentMinSec := fmt.Sprintf("%02d:%02d", now.Minute(), (sec/10)*10)
			if sec%10 == 1 && msIntoSec < 200 && currentMinSec != lastAnnouncedMinSec {
				lastAnnouncedMinSec = currentMinSec
				targetSec := ((sec/10)*10 + 10) % 60
				targetMin := now.Minute()
				targetHour := now.Hour()
				if targetSec == 0 {
					targetMin = (targetMin + 1) % 60
					if targetMin == 0 {
						targetHour = (targetHour + 1) % 24
					}
				}

				phrase := s.buildPhrase(lang, targetHour, targetMin, targetSec)
				currentPhrasePCM = s.synthesizeSpeech(phrase, lang)
				phraseOffset = 0
			}

			// Clear sample buffer
			for i := range pcmBuf {
				pcmBuf[i] = 0
			}

			// 1. Play synthesized speech voice if active
			if phraseOffset < len(currentPhrasePCM) {
				n := copy(pcmBuf, currentPhrasePCM[phraseOffset:])
				phraseOffset += n
			}

			// 2. Synthesize official Japanese 440Hz / 880Hz Time Signal Beeps
			// When sec % 10 is 7, 8, 9 -> 440Hz (A4) preview beeps ("ピッ", exact 100ms)
			// When sec % 10 is 0       -> 880Hz (A5) main time chime ("ポーーーン", 1800ms with natural decay)
			secMod10 := sec % 10
			for i := 0; i < chunkSamples; i++ {
				sampleMs := msIntoSec + (i * 1000 / SampleRate)
				var toneSample float64 = 0

				if (secMod10 == 7 || secMod10 == 8 || secMod10 == 9) && sampleMs < 100 {
					// 440 Hz preview beep (crisp 100ms with 5ms anti-click micro fades)
					phase := 2.0 * math.Pi * 440.0 * float64(sampleMs) / 1000.0
					env := 1.0
					if sampleMs < 5 {
						env = float64(sampleMs) / 5.0
					} else if sampleMs > 95 {
						env = float64(100-sampleMs) / 5.0
					}
					toneSample = math.Sin(phase) * env * 0.50
				} else if secMod10 == 0 && sampleMs < 1800 {
					// 880 Hz main time chime (1-octave higher A5 tone with sustained peak and smooth bell decay)
					phase := 2.0 * math.Pi * 880.0 * float64(sampleMs) / 1000.0
					env := 1.0
					if sampleMs < 5 {
						env = float64(sampleMs) / 5.0
					} else if sampleMs < 350 {
						// Sustained primary tone
						env = 1.0 - (float64(sampleMs-5)/350.0)*0.15
					} else {
						// Smooth exponential chime decay over remainder
						decayTime := float64(sampleMs-350) / 1450.0
						env = 0.85 * math.Exp(-decayTime*3.5)
					}
					toneSample = math.Sin(phase) * env * 0.55
				}

				if toneSample != 0 {
					pcmBuf[i] = int16(math.Max(-32768, math.Min(32767, float64(pcmBuf[i])+toneSample*32767)))
				}
			}

			// Convert int16 slice to little-endian bytes
			for i, v := range pcmBuf {
				binary.LittleEndian.PutUint16(byteBuf[i*2:], uint16(v))
			}

			if _, err := w.Write(byteBuf); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func (s *Service) buildPhrase(lang string, hour, min, sec int) string {
	if lang == "en" {
		ampm := "AM"
		h12 := hour
		if h12 >= 12 {
			ampm = "PM"
			if h12 > 12 {
				h12 -= 12
			}
		}
		if h12 == 0 {
			h12 = 12
		}
		if sec == 0 {
			return fmt.Sprintf("At the tone, the time will be %d %d %s.", h12, min, ampm)
		}
		return fmt.Sprintf("%d minutes and %d seconds.", min, sec)
	}

	// Japanese 117 phrasing with natural reading
	period := "午前"
	h12 := hour
	if h12 >= 12 {
		period = "午後"
		if h12 > 12 {
			h12 -= 12
		}
	}
	if h12 == 0 {
		h12 = 12
	}

	if sec == 0 {
		if min == 0 {
			return fmt.Sprintf("%s%d時ちょうどをお知らせします", period, h12)
		}
		return fmt.Sprintf("%s%d時%d分をお知らせします", period, h12, min)
	}
	return fmt.Sprintf("%d分%d秒をお知らせします", min, sec)
}

func (s *Service) synthesizeSpeech(text, lang string) []int16 {
	s.mu.Lock()
	cached, ok := s.voiceCache[text]
	s.mu.Unlock()
	if ok {
		return decodePCM(cached)
	}

	var rawWAV []byte

	// 1. Try open_jtalk if available
	if s.hasOpenJTalk {
		dicPaths := []string{"/var/lib/mecab/dic/open-jtalk/naist-jdic", "/usr/share/open-jtalk/dic", "/usr/local/dic"}
		// tohoku-f01-neutral: most popular natural female voice for Open JTalk (CC BY 4.0, Tohoku Univ.)
		// nitech paths kept as fallback for environments with system-installed open-jtalk
		voicePaths := []string{
			"/usr/local/voice/tohoku-f01-neutral.htsvoice",
			"/usr/share/hts-voice/tohoku-f01/tohoku-f01-neutral.htsvoice",
			"/usr/share/hts-voice/nitech-jp-atr503-m001/nitech_jp_atr503_m001.htsvoice",
			"/usr/local/voice/nitech_jp_atr503_m001.htsvoice",
		}

		var foundDic, foundVoice string
		for _, p := range dicPaths {
			if _, err := os.Stat(p); err == nil {
				foundDic = p
				break
			}
		}
		for _, p := range voicePaths {
			if _, err := os.Stat(p); err == nil {
				foundVoice = p
				break
			}
		}

		if foundDic != "" && foundVoice != "" {
			tmpWav := filepath.Join(os.TempDir(), fmt.Sprintf("mockcam_ojt_%d.wav", time.Now().UnixNano()))
			// Standard community-recommended parameters for natural female voice:
			//   -s 22050  sample rate (Hz)
			//   -r 1.25   speaking rate (clear and natural announcement cadence)
			//   -a 0.55   all-pass constant (spectral envelope, standard for tohoku-f01)
			//   -u 0.5    voiced/unvoiced threshold
			//   -fm 2.0   additional half-tones to raise pitch to a bright, pleasant tone
			cmd := exec.Command("open_jtalk",
				"-x", foundDic,
				"-m", foundVoice,
				"-ow", tmpWav,
				"-s", "22050",
				"-r", "1.25",
				"-a", "0.55",
				"-u", "0.5",
				"-fm", "2.0",
			)
			cmd.Stdin = strings.NewReader(text)
			if err := cmd.Run(); err == nil {
				if data, readErr := os.ReadFile(tmpWav); readErr == nil && len(data) > 44 {
					rawWAV = data
				}
				_ = os.Remove(tmpWav)
			}
		}
	}

	// 2. Try Windows PowerShell SAPI with preferred natural Japanese voice if on Windows
	if len(rawWAV) == 0 && s.hasPowerShell {
		tmpWav := filepath.Join(os.TempDir(), fmt.Sprintf("mockcam_voice_%d.wav", time.Now().UnixNano()))
		script := fmt.Sprintf(`
Add-Type -AssemblyName System.Speech;
$s = New-Object System.Speech.Synthesis.SpeechSynthesizer;
$s.Rate = 0;
$voice = $s.GetInstalledVoices() | Where-Object { $_.VoiceInfo.Culture -like "*ja*" } | Select-Object -First 1;
if ($voice) { $s.SelectVoice($voice.VoiceInfo.Name) }
$s.SetOutputToWaveFile('%s');
$s.Speak('%s');
$s.Dispose();
`, tmpWav, text)
		cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", script)
		if err := cmd.Run(); err == nil {
			if data, readErr := os.ReadFile(tmpWav); readErr == nil && len(data) > 44 {
				rawWAV = data
			}
			_ = os.Remove(tmpWav)
		}
	}

	// 3. Try espeak-ng / espeak (tuned with smoother cadence)
	if len(rawWAV) == 0 && s.hasEspeak {
		voice := "ja"
		speed := "130"
		pitch := "62"
		if lang == "en" {
			voice = "en-us"
			speed = "140"
			pitch = "55"
		}
		cmdName := "espeak-ng"
		if _, err := exec.LookPath(cmdName); err != nil {
			cmdName = "espeak"
		}
		cmd := exec.Command(cmdName, "-v", voice, "-s", speed, "-p", pitch, "-a", "100", text, "-w", "/dev/stdout")
		var out bytes.Buffer
		cmd.Stdout = &out
		if err := cmd.Run(); err == nil && out.Len() > 44 {
			rawWAV = out.Bytes()
		}
	}

	// 4. Fallback: Generate pleasant harmonic acoustic chime sequence
	if len(rawWAV) == 0 {
		pcm := generateChimeTones(text)
		return pcm
	}

	// Strip 44-byte WAV header if standard RIFF header present
	pcmData := rawWAV
	if len(rawWAV) > 44 && string(rawWAV[0:4]) == "RIFF" {
		pcmData = rawWAV[44:]
	}

	rawSamples := decodePCM(pcmData)
	trimmedSamples := trimSilence(rawSamples, 250)

	// Encode trimmed samples back for caching
	trimmedBytes := make([]byte, len(trimmedSamples)*2)
	for i, v := range trimmedSamples {
		binary.LittleEndian.PutUint16(trimmedBytes[i*2:], uint16(v))
	}

	s.mu.Lock()
	s.voiceCache[text] = trimmedBytes
	s.mu.Unlock()

	return trimmedSamples
}

func trimSilence(samples []int16, threshold int16) []int16 {
	if len(samples) == 0 {
		return samples
	}
	start := 0
	for start < len(samples) {
		val := samples[start]
		if val < 0 {
			val = -val
		}
		if val > threshold {
			break
		}
		start++
	}
	end := len(samples) - 1
	for end > start {
		val := samples[end]
		if val < 0 {
			val = -val
		}
		if val > threshold {
			break
		}
		end--
	}
	if start >= end {
		return samples
	}
	trimmed := samples[start : end+1]
	// Apply micro 10ms fade-in and fade-out to prevent clicks
	fadeLen := SampleRate * 10 / 1000
	if len(trimmed) > fadeLen*2 {
		for i := 0; i < fadeLen; i++ {
			trimmed[i] = int16(float64(trimmed[i]) * float64(i) / float64(fadeLen))
			lastIdx := len(trimmed) - 1 - i
			trimmed[lastIdx] = int16(float64(trimmed[lastIdx]) * float64(i) / float64(fadeLen))
		}
	}
	return trimmed
}

func decodePCM(pcmData []byte) []int16 {
	samples := make([]int16, len(pcmData)/2)
	for i := 0; i < len(samples); i++ {
		samples[i] = int16(binary.LittleEndian.Uint16(pcmData[i*2:]))
	}
	return samples
}

func generateChimeTones(text string) []int16 {
	// Synthesize multi-tone chime pattern (e.g. Westminster or melodic notification)
	notes := []float64{523.25, 659.25, 783.99, 1046.50} // C5, E5, G5, C6
	durationPerNote := 0.2                              // 200ms
	totalSamples := int(float64(len(notes)) * durationPerNote * SampleRate)
	pcm := make([]int16, totalSamples)

	for idx, freq := range notes {
		startSample := int(float64(idx) * durationPerNote * SampleRate)
		noteSamples := int(durationPerNote * SampleRate)
		for s := 0; s < noteSamples; s++ {
			t := float64(s) / SampleRate
			phase := 2.0 * math.Pi * freq * t
			env := math.Exp(-t / 0.12)
			val := math.Sin(phase)*env + 0.3*math.Sin(2*phase)*env
			pcm[startSample+s] = int16(val * 16000)
		}
	}
	return pcm
}
