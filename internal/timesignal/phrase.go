package timesignal

import (
	"fmt"
	"time"
)

// AnnouncePeriodSec is the interval, in seconds, between two spoken announcements.
// Each announcement names the upcoming 10-second mark, followed by preview
// pips at :07/:08/:09 and the mark tone at :00 of the next block.
const AnnouncePeriodSec = 10

// announceWindowStart / announceWindowEnd delimit (in seconds within a
// 10-second block) when a new announcement may start. Starting later than
// :03 would let the voice overlap the preview pips.
const (
	announceWindowStart = 1
	announceWindowEnd   = 3
)

// Announcement describes the spoken part of one 10-second block.
type Announcement struct {
	// Key uniquely identifies the block (minute + block index) so that a
	// streamer never speaks the same block twice.
	Key string
	// Hour, Minute, Second are the wall-clock values of the upcoming mark.
	Hour, Minute, Second int
}

// AnnouncementFor returns the announcement that should start at time now, and
// ok=false when now is outside the announcement window of its block.
func AnnouncementFor(now time.Time) (Announcement, bool) {
	sec := now.Second()
	pos := sec % AnnouncePeriodSec
	if pos < announceWindowStart || pos > announceWindowEnd {
		return Announcement{}, false
	}

	block := sec / AnnouncePeriodSec
	target := now.Truncate(time.Minute).Add(time.Duration(block+1) * AnnouncePeriodSec * time.Second)

	return Announcement{
		Key:    fmt.Sprintf("%02d:%02d:%d", now.Hour(), now.Minute(), block),
		Hour:   target.Hour(),
		Minute: target.Minute(),
		Second: target.Second(),
	}, true
}

// BuildPhrase renders the sentence spoken for the given target time.
//
// Japanese follows the NTT "117" phrasing loosely: the full time is announced
// on the minute, otherwise only minutes and seconds are read.
func BuildPhrase(lang string, hour, min, sec int) string {
	if lang == "en" {
		h12, ampm := to12Hour(hour)
		if sec == 0 {
			return fmt.Sprintf("At the tone, the time will be %d %02d %s.", h12, min, ampm)
		}
		return fmt.Sprintf("%d minutes and %d seconds.", min, sec)
	}

	h12, _ := to12Hour(hour)
	period := "午前"
	if hour >= 12 {
		period = "午後"
	}
	if sec == 0 {
		if min == 0 {
			return fmt.Sprintf("%s%d時ちょうどをお知らせします", period, h12)
		}
		return fmt.Sprintf("%s%d時%d分をお知らせします", period, h12, min)
	}
	return fmt.Sprintf("%d分%d秒をお知らせします", min, sec)
}

func to12Hour(hour int) (int, string) {
	ampm := "AM"
	h12 := hour
	if hour >= 12 {
		ampm = "PM"
		h12 -= 12
	}
	if h12 == 0 {
		h12 = 12
	}
	return h12, ampm
}
