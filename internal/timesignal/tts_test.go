package timesignal

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type call struct {
	name  string
	args  []string
	stdin string
}

// fakeRunner records invocations and simulates TTS binaries by writing a
// tiny WAV either to the "-ow" path (open_jtalk / SAPI) or to stdout.
type fakeRunner struct {
	available map[string]bool
	calls     []call
	fail      error
	rate      int
}

func newFakeRunner(bins ...string) *fakeRunner {
	f := &fakeRunner{available: map[string]bool{}, rate: 48000}
	for _, b := range bins {
		f.available[b] = true
	}
	return f
}

func (f *fakeRunner) LookPath(name string) bool { return f.available[name] }

func (f *fakeRunner) Run(_ context.Context, name string, args []string, stdin string) ([]byte, error) {
	f.calls = append(f.calls, call{name: name, args: args, stdin: stdin})
	if f.fail != nil {
		return nil, f.fail
	}
	wav := buildWAV(f.rate, 1, 16, [][]int{{0}, {8000}, {-8000}, {8000}, {0}})

	// Find an output path: "-ow <path>" for open_jtalk, or inside the SAPI script.
	for i, a := range args {
		if a == "-ow" && i+1 < len(args) {
			return nil, os.WriteFile(args[i+1], wav, 0o644)
		}
		if a == "-Command" && i+1 < len(args) {
			script := args[i+1]
			start := strings.Index(script, "SetOutputToWaveFile('") + len("SetOutputToWaveFile('")
			end := strings.Index(script[start:], "')")
			return nil, os.WriteFile(script[start:start+end], wav, 0o644)
		}
	}
	return wav, nil
}

func argValue(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func TestOpenJTalkArgsUseNativeRateAndNoPitchHacks(t *testing.T) {
	o := &OpenJTalk{DicPath: "/dic", VoicePath: "/voice.htsvoice", SpeechRate: 1.0}
	args := o.Args("/tmp/out.wav")

	if argValue(args, "-x") != "/dic" || argValue(args, "-m") != "/voice.htsvoice" || argValue(args, "-ow") != "/tmp/out.wav" {
		t.Fatalf("basic args wrong: %v", args)
	}
	if argValue(args, "-r") != "1.00" {
		t.Fatalf("speech rate should stay at the model default, got %q", argValue(args, "-r"))
	}
	for _, forbidden := range []string{"-s", "-a", "-fm", "-jm", "-u", "-b"} {
		if argValue(args, forbidden) != "" {
			t.Fatalf("flag %s must not be passed (it distorts the voice): %v", forbidden, args)
		}
	}

	// A zero/negative rate falls back to 1.0.
	o.SpeechRate = 0
	if argValue(o.Args("x"), "-r") != "1.00" {
		t.Fatal("zero rate should default to 1.00")
	}
}

func TestOpenJTalkSynthesize(t *testing.T) {
	r := newFakeRunner("open_jtalk")
	o := &OpenJTalk{Runner: r, DicPath: "/dic", VoicePath: "/voice", SpeechRate: 1.0, TempDir: t.TempDir()}

	pcm, err := o.Synthesize(context.Background(), "テスト", "ja")
	if err != nil {
		t.Fatal(err)
	}
	if pcm.SampleRate != 48000 || len(pcm.Samples) != 5 {
		t.Fatalf("pcm = rate %d len %d", pcm.SampleRate, len(pcm.Samples))
	}
	if len(r.calls) != 1 || r.calls[0].name != "open_jtalk" || r.calls[0].stdin != "テスト" {
		t.Fatalf("unexpected call: %+v", r.calls)
	}
	// Temp file must be cleaned up.
	entries, _ := os.ReadDir(o.TempDir)
	if len(entries) != 0 {
		t.Fatalf("temp wav not removed: %v", entries)
	}

	if _, err := o.Synthesize(context.Background(), "hello", "en"); err == nil {
		t.Fatal("open_jtalk must reject English")
	}

	r.fail = errors.New("boom")
	if _, err := o.Synthesize(context.Background(), "テスト", "ja"); err == nil {
		t.Fatal("runner failure must propagate")
	}
}

func TestNewOpenJTalkDetection(t *testing.T) {
	// Not on PATH → nil.
	if NewOpenJTalk(newFakeRunner()) != nil {
		t.Fatal("missing binary should disable open_jtalk")
	}

	// On PATH but no dictionary/voice → nil.
	t.Setenv("MOCKCAM_OPENJTALK_DIC", "")
	t.Setenv("MOCKCAM_OPENJTALK_VOICE", "")
	if runtime.GOOS == "windows" && NewOpenJTalk(newFakeRunner("open_jtalk")) != nil {
		t.Fatal("no dictionary should disable open_jtalk")
	}

	// Environment overrides pointing at existing files → enabled.
	dir := t.TempDir()
	dic := filepath.Join(dir, "dic")
	voice := filepath.Join(dir, "v.htsvoice")
	_ = os.Mkdir(dic, 0o755)
	_ = os.WriteFile(voice, []byte("x"), 0o644)
	t.Setenv("MOCKCAM_OPENJTALK_DIC", dic)
	t.Setenv("MOCKCAM_OPENJTALK_VOICE", voice)
	o := NewOpenJTalk(newFakeRunner("open_jtalk"))
	if o == nil || o.DicPath != dic || o.VoicePath != voice {
		t.Fatalf("env override not honoured: %+v", o)
	}
}

func TestESpeak(t *testing.T) {
	if NewESpeak(newFakeRunner()) != nil {
		t.Fatal("missing espeak should return nil")
	}
	e := NewESpeak(newFakeRunner("espeak"))
	if e == nil || e.Binary != "espeak" {
		t.Fatalf("legacy espeak should be picked: %+v", e)
	}
	e = NewESpeak(newFakeRunner("espeak-ng", "espeak"))
	if e.Binary != "espeak-ng" {
		t.Fatal("espeak-ng should be preferred")
	}

	args := e.Args("hello", "en")
	if argValue(args, "-v") != "en-us" || args[len(args)-1] != "hello" || !contains(args, "--stdout") {
		t.Fatalf("english args: %v", args)
	}
	if argValue(e.Args("こんにちは", "ja"), "-v") != "ja" {
		t.Fatal("japanese voice")
	}

	pcm, err := e.Synthesize(context.Background(), "hello", "en")
	if err != nil || len(pcm.Samples) != 5 {
		t.Fatalf("synthesize: err=%v len=%d", err, len(pcm.Samples))
	}
	if _, err := e.Synthesize(context.Background(), "x", "fr"); err == nil {
		t.Fatal("unsupported language")
	}
}

func TestWindowsSAPIScriptEscaping(t *testing.T) {
	w := &WindowsSAPI{}
	script := w.Script("it's 5 o'clock", "en", `C:\tmp\o'ut.wav`)
	if !strings.Contains(script, "$s.Speak('it''s 5 o''clock')") {
		t.Fatalf("single quotes must be doubled:\n%s", script)
	}
	if !strings.Contains(script, `SetOutputToWaveFile('C:\tmp\o''ut.wav')`) {
		t.Fatalf("path must be escaped:\n%s", script)
	}
	if !strings.Contains(script, "-like 'en*'") {
		t.Fatal("culture filter missing")
	}
	if !strings.Contains(w.Script("x", "ja", "p"), "-like 'ja*'") {
		t.Fatal("japanese culture filter missing")
	}
}

func TestWindowsSAPISynthesize(t *testing.T) {
	r := newFakeRunner("powershell")
	w := &WindowsSAPI{Runner: r, TempDir: t.TempDir()}
	pcm, err := w.Synthesize(context.Background(), "hello", "en")
	if err != nil || len(pcm.Samples) != 5 {
		t.Fatalf("err=%v len=%d", err, len(pcm.Samples))
	}
	if r.calls[0].name != "powershell" || !contains(r.calls[0].args, "-NonInteractive") {
		t.Fatalf("unexpected call: %+v", r.calls[0])
	}
}

func TestChimeFallback(t *testing.T) {
	pcm, err := Chime{SampleRate: 48000}.Synthesize(context.Background(), "", "zz")
	if err != nil || pcm.SampleRate != 48000 || len(pcm.Samples) == 0 {
		t.Fatalf("chime: err=%v rate=%d len=%d", err, pcm.SampleRate, len(pcm.Samples))
	}
	if r := rms(pcm.Samples); r < 0.05 {
		t.Fatalf("chime too quiet: %v", r)
	}
	if pcm.Samples[0] != 0 {
		t.Fatal("chime should start from silence (no click)")
	}
}

type stubSynth struct {
	name  string
	langs []string
	pcm   PCM
	err   error
	calls int
}

func (s *stubSynth) Name() string { return s.name }
func (s *stubSynth) Supports(lang string) bool {
	for _, l := range s.langs {
		if l == lang {
			return true
		}
	}
	return false
}
func (s *stubSynth) Synthesize(context.Context, string, string) (PCM, error) {
	s.calls++
	return s.pcm, s.err
}

func TestChainOrderAndFallback(t *testing.T) {
	ja := &stubSynth{name: "ja-only", langs: []string{"ja"}, err: errors.New("down")}
	empty := &stubSynth{name: "empty", langs: []string{"ja", "en"}, pcm: PCM{SampleRate: 48000}}
	good := &stubSynth{name: "good", langs: []string{"ja", "en"}, pcm: PCM{SampleRate: 48000, Samples: []int16{1}}}
	chain := Chain{ja, empty, good}

	if chain.Name() != "ja-only>empty>good" {
		t.Fatalf("name = %q", chain.Name())
	}
	if !chain.Supports("en") || chain.Supports("de") {
		t.Fatal("Supports should be the union of members")
	}

	pcm, err := chain.Synthesize(context.Background(), "x", "ja")
	if err != nil || len(pcm.Samples) != 1 {
		t.Fatalf("chain should reach the good backend: err=%v", err)
	}
	if ja.calls != 1 || empty.calls != 1 || good.calls != 1 {
		t.Fatalf("calls: ja=%d empty=%d good=%d", ja.calls, empty.calls, good.calls)
	}

	// English skips the ja-only backend.
	_, _ = chain.Synthesize(context.Background(), "x", "en")
	if ja.calls != 1 {
		t.Fatal("ja-only backend must not be called for English")
	}

	// All failing → error mentions each backend.
	failing := Chain{ja, empty}
	if _, err := failing.Synthesize(context.Background(), "x", "ja"); err == nil || !strings.Contains(err.Error(), "ja-only") || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("aggregated error missing backend names: %v", err)
	}
	jaOnly := Chain{ja}
	if _, err := jaOnly.Synthesize(context.Background(), "x", "en"); err == nil {
		t.Fatal("no backend for language should error")
	}
}

func TestDetectSynthesizersAlwaysEndsWithChime(t *testing.T) {
	t.Setenv("MOCKCAM_OPENJTALK_DIC", "")
	t.Setenv("MOCKCAM_OPENJTALK_VOICE", "")
	chain := DetectSynthesizers(newFakeRunner())
	if len(chain) == 0 {
		t.Fatal("chain empty")
	}
	last := chain[len(chain)-1]
	if _, ok := last.(Chime); !ok {
		t.Fatalf("last backend should be the chime fallback, got %s", last.Name())
	}

	chain = DetectSynthesizers(newFakeRunner("espeak-ng"))
	if !strings.Contains(chain.Name(), "espeak-ng") {
		t.Fatalf("espeak-ng should be detected: %s", chain.Name())
	}
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
