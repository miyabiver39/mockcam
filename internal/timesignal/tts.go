package timesignal

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Synthesizer turns a sentence into mono PCM. Implementations return
// (PCM{}, error) when they cannot handle the language or the backend fails,
// in which case the chain tries the next one.
type Synthesizer interface {
	Name() string
	Supports(lang string) bool
	Synthesize(ctx context.Context, text, lang string) (PCM, error)
}

// Runner executes an external program. It is an interface so backends can
// be unit-tested with a fake that never spawns processes.
type Runner interface {
	// Run executes name with args, feeding stdin, and returns stdout.
	Run(ctx context.Context, name string, args []string, stdin string) ([]byte, error)
	// LookPath reports whether name is available on PATH.
	LookPath(name string) bool
}

// ExecRunner is the production Runner backed by os/exec.
type ExecRunner struct{}

// Run implements Runner.
func (ExecRunner) Run(ctx context.Context, name string, args []string, stdin string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s: %w (%s)", name, err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// LookPath implements Runner.
func (ExecRunner) LookPath(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// synthTimeout bounds a single TTS invocation.
const synthTimeout = 10 * time.Second

// ---------------------------------------------------------------------------
// Open JTalk (Japanese)
// ---------------------------------------------------------------------------

// Default search locations for the dictionary and voice model. They can be
// overridden with MOCKCAM_OPENJTALK_DIC / MOCKCAM_OPENJTALK_VOICE.
var (
	openJTalkDicPaths = []string{
		"/usr/local/dic",
		"/var/lib/mecab/dic/open-jtalk/naist-jdic",
		"/usr/share/open-jtalk/dic",
	}
	openJTalkVoicePaths = []string{
		"/usr/local/voice/tohoku-f01-neutral.htsvoice",
		"/usr/share/hts-voice/tohoku-f01/tohoku-f01-neutral.htsvoice",
		"/usr/share/hts-voice/nitech-jp-atr503-m001/nitech_jp_atr503_m001.htsvoice",
		"/usr/local/voice/nitech_jp_atr503_m001.htsvoice",
	}
)

// OpenJTalk synthesizes Japanese speech with the open_jtalk CLI.
//
// Parameter policy: the HTS voice models shipped with MockCam (tohoku-f01,
// nitech) are trained at 48 kHz with a specific mel-cepstral all-pass
// constant (alpha). Overriding the sampling rate (-s) without a matching
// alpha (-a) warps the spectral envelope and makes the voice sound hollow
// and unnatural, which is exactly what earlier versions did (-s 22050 with
// -a 0.55, then compensating with -fm/-r). We therefore let open_jtalk run
// at the model's native rate with its built-in alpha and resample in Go.
// The only tuning applied is a gentle speech rate for announcement cadence.
type OpenJTalk struct {
	Runner    Runner
	DicPath   string
	VoicePath string
	// SpeechRate maps to -r (1.0 = model default). Kept close to 1.0 so
	// consonants are not smeared.
	SpeechRate float64
	// TempDir is where the intermediate WAV is written (os.TempDir by default).
	TempDir string
}

// NewOpenJTalk locates the dictionary/voice and returns nil when the backend
// is unusable on this machine.
func NewOpenJTalk(r Runner) *OpenJTalk {
	if !r.LookPath("open_jtalk") {
		return nil
	}
	dic := firstExisting(os.Getenv("MOCKCAM_OPENJTALK_DIC"), openJTalkDicPaths)
	voice := firstExisting(os.Getenv("MOCKCAM_OPENJTALK_VOICE"), openJTalkVoicePaths)
	if dic == "" || voice == "" {
		return nil
	}
	return &OpenJTalk{Runner: r, DicPath: dic, VoicePath: voice, SpeechRate: 1.0}
}

// Name implements Synthesizer.
func (o *OpenJTalk) Name() string { return "open_jtalk" }

// Supports implements Synthesizer.
func (o *OpenJTalk) Supports(lang string) bool { return lang == "ja" }

// Args returns the command-line arguments used for a synthesis writing to outPath.
func (o *OpenJTalk) Args(outPath string) []string {
	rate := o.SpeechRate
	if rate <= 0 {
		rate = 1.0
	}
	return []string{
		"-x", o.DicPath,
		"-m", o.VoicePath,
		"-r", fmt.Sprintf("%.2f", rate),
		"-ow", outPath,
	}
}

// Synthesize implements Synthesizer.
func (o *OpenJTalk) Synthesize(ctx context.Context, text, lang string) (PCM, error) {
	if !o.Supports(lang) {
		return PCM{}, errors.New("open_jtalk: unsupported language")
	}
	dir := o.TempDir
	if dir == "" {
		dir = os.TempDir()
	}
	outPath := filepath.Join(dir, fmt.Sprintf("mockcam_ojt_%d.wav", time.Now().UnixNano()))
	defer os.Remove(outPath)

	ctx, cancel := context.WithTimeout(ctx, synthTimeout)
	defer cancel()
	if _, err := o.Runner.Run(ctx, "open_jtalk", o.Args(outPath), text); err != nil {
		return PCM{}, err
	}
	data, err := os.ReadFile(outPath)
	if err != nil {
		return PCM{}, fmt.Errorf("open_jtalk: read output: %w", err)
	}
	return ParseWAV(data)
}

// ---------------------------------------------------------------------------
// espeak-ng / espeak (English, Japanese fallback)
// ---------------------------------------------------------------------------

// ESpeak synthesizes speech with espeak-ng (or the legacy espeak binary).
type ESpeak struct {
	Runner Runner
	Binary string
}

// NewESpeak returns nil when neither espeak-ng nor espeak is installed.
func NewESpeak(r Runner) *ESpeak {
	for _, bin := range []string{"espeak-ng", "espeak"} {
		if r.LookPath(bin) {
			return &ESpeak{Runner: r, Binary: bin}
		}
	}
	return nil
}

// Name implements Synthesizer.
func (e *ESpeak) Name() string { return e.Binary }

// Supports implements Synthesizer.
func (e *ESpeak) Supports(lang string) bool { return lang == "en" || lang == "ja" }

// Args returns the espeak command line for text in lang. Output goes to stdout.
func (e *ESpeak) Args(text, lang string) []string {
	voice, speed, pitch := "ja", "130", "60"
	if lang == "en" {
		voice, speed, pitch = "en-us", "150", "50"
	}
	return []string{"-v", voice, "-s", speed, "-p", pitch, "-a", "110", "--stdout", text}
}

// Synthesize implements Synthesizer.
func (e *ESpeak) Synthesize(ctx context.Context, text, lang string) (PCM, error) {
	if !e.Supports(lang) {
		return PCM{}, errors.New("espeak: unsupported language")
	}
	ctx, cancel := context.WithTimeout(ctx, synthTimeout)
	defer cancel()
	out, err := e.Runner.Run(ctx, e.Binary, e.Args(text, lang), "")
	if err != nil {
		return PCM{}, err
	}
	return ParseWAV(out)
}

// ---------------------------------------------------------------------------
// Windows SAPI via PowerShell
// ---------------------------------------------------------------------------

// WindowsSAPI uses System.Speech through PowerShell. It picks the first
// installed voice matching the requested culture.
type WindowsSAPI struct {
	Runner  Runner
	TempDir string
}

// NewWindowsSAPI returns nil on non-Windows platforms or when powershell is missing.
func NewWindowsSAPI(r Runner) *WindowsSAPI {
	if runtime.GOOS != "windows" || !r.LookPath("powershell") {
		return nil
	}
	return &WindowsSAPI{Runner: r}
}

// Name implements Synthesizer.
func (w *WindowsSAPI) Name() string { return "sapi" }

// Supports implements Synthesizer.
func (w *WindowsSAPI) Supports(lang string) bool { return lang == "en" || lang == "ja" }

// Script returns the PowerShell program that writes text to outPath.
func (w *WindowsSAPI) Script(text, lang, outPath string) string {
	culture := "ja"
	if lang == "en" {
		culture = "en"
	}
	// Single quotes inside PowerShell single-quoted strings are doubled.
	esc := func(s string) string { return strings.ReplaceAll(s, "'", "''") }
	return fmt.Sprintf(`
Add-Type -AssemblyName System.Speech
$s = New-Object System.Speech.Synthesis.SpeechSynthesizer
$s.Rate = 0
$voice = $s.GetInstalledVoices() | Where-Object { $_.VoiceInfo.Culture.Name -like '%s*' } | Select-Object -First 1
if ($voice) { $s.SelectVoice($voice.VoiceInfo.Name) }
$s.SetOutputToWaveFile('%s')
$s.Speak('%s')
$s.Dispose()
`, culture, esc(outPath), esc(text))
}

// Synthesize implements Synthesizer.
func (w *WindowsSAPI) Synthesize(ctx context.Context, text, lang string) (PCM, error) {
	if !w.Supports(lang) {
		return PCM{}, errors.New("sapi: unsupported language")
	}
	dir := w.TempDir
	if dir == "" {
		dir = os.TempDir()
	}
	outPath := filepath.Join(dir, fmt.Sprintf("mockcam_sapi_%d.wav", time.Now().UnixNano()))
	defer os.Remove(outPath)

	ctx, cancel := context.WithTimeout(ctx, synthTimeout)
	defer cancel()
	args := []string{"-NoProfile", "-NonInteractive", "-Command", w.Script(text, lang, outPath)}
	if _, err := w.Runner.Run(ctx, "powershell", args, ""); err != nil {
		return PCM{}, err
	}
	data, err := os.ReadFile(outPath)
	if err != nil {
		return PCM{}, fmt.Errorf("sapi: read output: %w", err)
	}
	return ParseWAV(data)
}

// ---------------------------------------------------------------------------
// Chime fallback (no TTS available)
// ---------------------------------------------------------------------------

// Chime is the last-resort synthesizer: a short ascending C-major arpeggio
// that at least marks the announcement slot when no TTS engine exists.
type Chime struct {
	SampleRate int
}

// Name implements Synthesizer.
func (Chime) Name() string { return "chime" }

// Supports implements Synthesizer.
func (Chime) Supports(string) bool { return true }

// Synthesize implements Synthesizer.
func (c Chime) Synthesize(_ context.Context, _ string, _ string) (PCM, error) {
	rate := c.SampleRate
	if rate <= 0 {
		rate = SampleRate
	}
	notes := []float64{523.25, 659.25, 783.99, 1046.50} // C5 E5 G5 C6
	const noteSec = 0.2
	noteSamples := int(noteSec * float64(rate))
	out := make([]int16, noteSamples*len(notes))
	for n, freq := range notes {
		for i := 0; i < noteSamples; i++ {
			t := float64(i) / float64(rate)
			env := math.Exp(-t/0.12) * raisedCosineRamp(t/0.005)
			v := (math.Sin(2*math.Pi*freq*t) + 0.3*math.Sin(4*math.Pi*freq*t)) * env
			out[n*noteSamples+i] = clampSample(v * 0.5 * 32767)
		}
	}
	return PCM{SampleRate: rate, Samples: out}, nil
}

// ---------------------------------------------------------------------------
// Chain
// ---------------------------------------------------------------------------

// Chain tries synthesizers in order and returns the first successful result.
type Chain []Synthesizer

// Name implements Synthesizer.
func (c Chain) Name() string {
	names := make([]string, 0, len(c))
	for _, s := range c {
		names = append(names, s.Name())
	}
	return strings.Join(names, ">")
}

// Supports implements Synthesizer.
func (c Chain) Supports(lang string) bool {
	for _, s := range c {
		if s.Supports(lang) {
			return true
		}
	}
	return false
}

// Synthesize implements Synthesizer.
func (c Chain) Synthesize(ctx context.Context, text, lang string) (PCM, error) {
	var errs []error
	for _, s := range c {
		if !s.Supports(lang) {
			continue
		}
		pcm, err := s.Synthesize(ctx, text, lang)
		if err == nil && len(pcm.Samples) > 0 {
			return pcm, nil
		}
		if err == nil {
			err = errors.New("empty output")
		}
		errs = append(errs, fmt.Errorf("%s: %w", s.Name(), err))
	}
	if len(errs) == 0 {
		return PCM{}, fmt.Errorf("no synthesizer supports language %q", lang)
	}
	return PCM{}, errors.Join(errs...)
}

// DetectSynthesizers builds the default backend chain for this machine:
// Open JTalk (ja) → Windows SAPI → espeak → chime fallback.
func DetectSynthesizers(r Runner) Chain {
	var chain Chain
	if ojt := NewOpenJTalk(r); ojt != nil {
		chain = append(chain, ojt)
	}
	if sapi := NewWindowsSAPI(r); sapi != nil {
		chain = append(chain, sapi)
	}
	if es := NewESpeak(r); es != nil {
		chain = append(chain, es)
	}
	chain = append(chain, Chime{SampleRate: SampleRate})
	return chain
}

func firstExisting(override string, candidates []string) string {
	if override != "" {
		if _, err := os.Stat(override); err == nil {
			return override
		}
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}
