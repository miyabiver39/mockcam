package timesignal

import (
	"testing"
	"time"
)

func TestBuildPhraseJapanese(t *testing.T) {
	cases := []struct {
		h, m, s int
		want    string
	}{
		{0, 0, 0, "午前12時ちょうどをお知らせします"},
		{9, 0, 0, "午前9時ちょうどをお知らせします"},
		{12, 0, 0, "午後12時ちょうどをお知らせします"},
		{13, 5, 0, "午後1時5分をお知らせします"},
		{23, 59, 0, "午後11時59分をお知らせします"},
		{13, 5, 10, "5分10秒をお知らせします"},
		{7, 30, 50, "30分50秒をお知らせします"},
	}
	for _, tc := range cases {
		if got := BuildPhrase("ja", tc.h, tc.m, tc.s); got != tc.want {
			t.Errorf("BuildPhrase(ja,%d,%d,%d) = %q, want %q", tc.h, tc.m, tc.s, got, tc.want)
		}
	}
	// Unknown languages default to Japanese.
	if got := BuildPhrase("xx", 13, 5, 0); got != "午後1時5分をお知らせします" {
		t.Errorf("unknown lang should fall back to Japanese, got %q", got)
	}
}

func TestBuildPhraseEnglish(t *testing.T) {
	cases := []struct {
		h, m, s int
		want    string
	}{
		{0, 0, 0, "At the tone, the time will be 12 00 AM."},
		{12, 7, 0, "At the tone, the time will be 12 07 PM."},
		{15, 45, 0, "At the tone, the time will be 3 45 PM."},
		{15, 45, 20, "45 minutes and 20 seconds."},
	}
	for _, tc := range cases {
		if got := BuildPhrase("en", tc.h, tc.m, tc.s); got != tc.want {
			t.Errorf("BuildPhrase(en,%d,%d,%d) = %q, want %q", tc.h, tc.m, tc.s, got, tc.want)
		}
	}
}

func TestAnnouncementFor(t *testing.T) {
	base := time.Date(2026, 9, 15, 13, 25, 0, 0, time.UTC)

	// :21 belongs to block 2 (20-29) and announces the :30 mark.
	ann, ok := AnnouncementFor(base.Add(21 * time.Second))
	if !ok {
		t.Fatal("expected announcement at :21")
	}
	if ann.Hour != 13 || ann.Minute != 25 || ann.Second != 30 {
		t.Fatalf("target = %02d:%02d:%02d, want 13:25:30", ann.Hour, ann.Minute, ann.Second)
	}
	if ann.Key != "13:25:2" {
		t.Fatalf("key = %q", ann.Key)
	}

	// The whole window :21-:23 maps to the same key.
	for _, off := range []time.Duration{21 * time.Second, 22*time.Second + 500*time.Millisecond, 23*time.Second + 999*time.Millisecond} {
		a, ok := AnnouncementFor(base.Add(off))
		if !ok || a.Key != ann.Key {
			t.Fatalf("offset %v: ok=%v key=%q, want key %q", off, ok, a.Key, ann.Key)
		}
	}

	// Outside the window nothing should start.
	for _, off := range []time.Duration{20 * time.Second, 24 * time.Second, 27 * time.Second, 29 * time.Second} {
		if _, ok := AnnouncementFor(base.Add(off)); ok {
			t.Fatalf("offset %v should not start an announcement", off)
		}
	}

	// :51 announces the next minute's :00 (minute rollover).
	ann, ok = AnnouncementFor(base.Add(51 * time.Second))
	if !ok || ann.Hour != 13 || ann.Minute != 26 || ann.Second != 0 {
		t.Fatalf("rollover: ok=%v target=%02d:%02d:%02d", ok, ann.Hour, ann.Minute, ann.Second)
	}

	// 23:59:51 announces 00:00:00 (day rollover).
	ann, ok = AnnouncementFor(time.Date(2026, 9, 15, 23, 59, 51, 0, time.UTC))
	if !ok || ann.Hour != 0 || ann.Minute != 0 || ann.Second != 0 {
		t.Fatalf("day rollover: ok=%v target=%02d:%02d:%02d", ok, ann.Hour, ann.Minute, ann.Second)
	}
}
