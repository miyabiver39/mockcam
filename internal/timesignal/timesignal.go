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
			// Announce every 10 seconds: e.g. at :02, :12, :22, :32, :42, :52
			currentMinSec := fmt.Sprintf("%02d:%02d", now.Minute(), (sec/10)*10)
			if sec%10 == 2 && msIntoSec < 200 && currentMinSec != lastAnnouncedMinSec {
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

			// 2. Synthesize authentic 117 Time Signal Chime
			// When sec % 10 is 7, 8, 9 -> 440Hz tone (200ms)
			// When sec % 10 is 0 -> 880Hz tone (700ms)
			secMod10 := sec % 10
			for i := 0; i < chunkSamples; i++ {
				sampleMs := msIntoSec + (i * 1000 / SampleRate)
				var toneSample float64 = 0

				if (secMod10 == 7 || secMod10 == 8 || secMod10 == 9) && sampleMs < 200 {
					// 440 Hz preview tone with smooth envelope
					phase := 2.0 * math.Pi * 440.0 * float64(sampleMs) / 1000.0
					envelope := math.Sin(float64(sampleMs) / 200.0 * math.Pi)
					toneSample = math.Sin(phase) * envelope * 0.45
				} else if secMod10 == 0 && sampleMs < 700 {
					// 880 Hz main time tone with exponential decay
					phase := 2.0 * math.Pi * 880.0 * float64(sampleMs) / 1000.0
					envelope := math.Exp(-float64(sampleMs) / 350.0)
					toneSample = math.Sin(phase) * envelope * 0.55
				} else if sampleMs < 15 {
					// Subtle 1-second ticking pulse at top of each second
					tickPhase := 2.0 * math.Pi * 1200.0 * float64(sampleMs) / 1000.0
					tickEnv := (1.0 - float64(sampleMs)/15.0)
					toneSample = math.Sin(tickPhase) * tickEnv * 0.08
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

	// Natural Japanese hours
	hourWords := map[int]string{
		1: "いちじ", 2: "にじ", 3: "さんじ", 4: "よじ", 5: "ごじ", 6: "ろくじ",
		7: "しちじ", 8: "はちじ", 9: "くじ", 10: "じゅうじ", 11: "じゅういちじ", 12: "じゅうにじ",
	}
	hStr := hourWords[h12]
	if hStr == "" {
		hStr = fmt.Sprintf("%dじ", h12)
	}

	// Natural Japanese minutes
	minStr := formatJapaneseMinutes(min)

	if sec == 0 {
		return fmt.Sprintf("%s、%s、%sをお知らせします。", period, hStr, minStr)
	}
	return fmt.Sprintf("%s、%d秒をお知らせします。", minStr, sec)
}

func formatJapaneseMinutes(m int) string {
	if m == 0 {
		return "ちょうと"
	}
	units := []string{"", "いっぷん", "にふん", "さんぷん", "よんぷん", "ごふん", "ろっぷん", "ななふん", "はっぷん", "きゅうふん"}
	tens := []string{"", "じゅう", "にじゅう", "さんじゅう", "よんじゅう", "ごじゅう"}

	t := m / 10
	u := m % 10

	if u == 0 {
		return tens[t] + "っぷん"
	}
	return tens[t] + units[u]
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
		// Common dictionary and voice paths
		dicPaths := []string{"/var/lib/mecab/dic/open-jtalk/naist-jdic", "/usr/share/open-jtalk/dic", "/usr/local/dic"}
		voicePaths := []string{"/usr/share/hts-voice/nitech-jp-atr503-m001/nitech_jp_atr503_m001.htsvoice", "/usr/local/voice/nitech_jp_atr503_m001.htsvoice"}

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
			cmd := exec.Command("open_jtalk", "-x", foundDic, "-m", foundVoice, "-ow", tmpWav)
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
		pitch := "52"
		if lang == "en" {
			voice = "en-us"
			speed = "140"
			pitch = "50"
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

	s.mu.Lock()
	s.voiceCache[text] = pcmData
	s.mu.Unlock()

	return decodePCM(pcmData)
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
