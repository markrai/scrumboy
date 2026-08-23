package ics

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestExpandAllDayExclusiveDTEND(t *testing.T) {
	loc := time.UTC
	day := time.Date(2026, 8, 17, 0, 0, 0, 0, loc)
	body := []byte(`BEGIN:VCALENDAR
VERSION:2.0
BEGIN:VEVENT
UID:all-day
DTSTART;VALUE=DATE:20260817
DTEND;VALUE=DATE:20260818
SUMMARY:Holiday
END:VEVENT
END:VCALENDAR
`)
	events, err := Expand(body, loc, day, day.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if len(events) != 1 || events[0].Title != "Holiday" || !events[0].AllDay {
		t.Fatalf("events=%+v", events)
	}
	next, err := Expand(body, loc, day.Add(24*time.Hour), day.Add(48*time.Hour))
	if err != nil {
		t.Fatalf("Expand next day: %v", err)
	}
	if len(next) != 0 {
		t.Fatalf("exclusive DTEND leaked into next day: %+v", next)
	}
}

func TestExpandMidnightSpanningTimedEvent(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 8, 17, 0, 0, 0, 0, loc)
	body := []byte(`BEGIN:VCALENDAR
VERSION:2.0
BEGIN:VEVENT
UID:late
DTSTART;TZID=America/New_York:20260817T230000
DTEND;TZID=America/New_York:20260818T010000
SUMMARY:Late
END:VEVENT
END:VCALENDAR
`)
	today, err := Expand(body, loc, day, day.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("Expand today: %v", err)
	}
	if len(today) != 1 {
		t.Fatalf("today=%+v", today)
	}
	tomorrow, err := Expand(body, loc, day.Add(24*time.Hour), day.Add(48*time.Hour))
	if err != nil {
		t.Fatalf("Expand tomorrow: %v", err)
	}
	if len(tomorrow) != 1 {
		t.Fatalf("tomorrow=%+v", tomorrow)
	}
}

func TestExpandFloatingUsesBoardTimezone(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 8, 17, 0, 0, 0, 0, loc)
	body := []byte(`BEGIN:VCALENDAR
VERSION:2.0
BEGIN:VEVENT
UID:float
DTSTART:20260817T090000
DTEND:20260817T100000
SUMMARY:Floating
END:VEVENT
END:VCALENDAR
`)
	events, err := Expand(body, loc, day, day.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events=%+v", events)
	}
	got := events[0].StartsAt.In(loc)
	if got.Hour() != 9 {
		t.Fatalf("floating start hour=%d, want 9 in board TZ", got.Hour())
	}
}

func TestExpandUnknownTZIDFailsClosed(t *testing.T) {
	body := []byte(`BEGIN:VCALENDAR
VERSION:2.0
BEGIN:VEVENT
UID:bad-tz
DTSTART;TZID=Not/A_Zone:20260817T100000
DTEND;TZID=Not/A_Zone:20260817T110000
SUMMARY:Nope
END:VEVENT
END:VCALENDAR
`)
	_, err := Expand(body, time.UTC, time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC), time.Date(2026, 8, 18, 0, 0, 0, 0, time.UTC))
	if !errors.Is(err, ErrUnknownTimezone) {
		t.Fatalf("err=%v, want ErrUnknownTimezone", err)
	}
}

func TestExpandUTCConvertedIntoBoardZoneDay(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 8, 17, 0, 0, 0, 0, loc)
	body := []byte(`BEGIN:VCALENDAR
VERSION:2.0
BEGIN:VEVENT
UID:utc
DTSTART:20260817T160000Z
DTEND:20260817T170000Z
SUMMARY:UTC
END:VEVENT
END:VCALENDAR
`)
	events, err := Expand(body, loc, day, day.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events=%+v", events)
	}
	local := events[0].StartsAt.In(loc)
	if local.Day() != 17 || local.Hour() != 12 {
		t.Fatalf("UTC conversion local=%s, want Aug 17 12:00", local)
	}

	early := []byte(`BEGIN:VCALENDAR
VERSION:2.0
BEGIN:VEVENT
UID:early-utc
DTSTART:20260817T020000Z
DTEND:20260817T030000Z
SUMMARY:Early UTC
END:VEVENT
END:VCALENDAR
`)
	onLocal17, err := Expand(early, loc, day, day.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("Expand local 17: %v", err)
	}
	if len(onLocal17) != 0 {
		t.Fatalf("02:00Z is previous local evening, want 0 on Aug 17, got %+v", onLocal17)
	}
	prev := time.Date(2026, 8, 16, 0, 0, 0, 0, loc)
	onLocal16, err := Expand(early, loc, prev, day)
	if err != nil {
		t.Fatalf("Expand local 16: %v", err)
	}
	if len(onLocal16) != 1 {
		t.Fatalf("02:00Z should land on Aug 16 local, got %+v", onLocal16)
	}
}

func TestExpandDropsCancelledAndExpandsRRuleWithException(t *testing.T) {
	loc := time.UTC
	windowStart := time.Date(2026, 8, 17, 0, 0, 0, 0, loc)
	windowEnd := time.Date(2026, 8, 22, 0, 0, 0, 0, loc)
	body := []byte(`BEGIN:VCALENDAR
VERSION:2.0
BEGIN:VEVENT
UID:series
DTSTART:20260817T150000Z
DTEND:20260817T160000Z
RRULE:FREQ=DAILY;COUNT=5
SUMMARY:Standup
END:VEVENT
BEGIN:VEVENT
UID:series
RECURRENCE-ID:20260819T150000Z
DTSTART:20260819T170000Z
DTEND:20260819T180000Z
SUMMARY:Standup moved
END:VEVENT
BEGIN:VEVENT
UID:cancelled
DTSTART:20260817T120000Z
DTEND:20260817T130000Z
STATUS:CANCELLED
SUMMARY:Skip me
END:VEVENT
END:VCALENDAR
`)
	events, err := Expand(body, loc, windowStart, windowEnd)
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	var titles []string
	for _, ev := range events {
		titles = append(titles, ev.Title+"@"+ev.StartsAt.UTC().Format("02T15"))
	}
	joined := strings.Join(titles, ",")
	if strings.Contains(joined, "Skip me") {
		t.Fatalf("cancelled event leaked: %s", joined)
	}
	if !strings.Contains(joined, "Standup moved@19T17") {
		t.Fatalf("missing recurrence override: %s", joined)
	}
	if strings.Contains(joined, "Standup@19T15") {
		t.Fatalf("original occurrence not replaced: %s", joined)
	}
}

func TestExpandDSTSpringForward(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 3, 8, 0, 0, 0, 0, loc)
	body := []byte(`BEGIN:VCALENDAR
VERSION:2.0
BEGIN:VEVENT
UID:dst
DTSTART;TZID=America/New_York:20260308T013000
DTEND;TZID=America/New_York:20260308T030000
SUMMARY:DST
END:VEVENT
END:VCALENDAR
`)
	events, err := Expand(body, loc, day, day.AddDate(0, 0, 1))
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events=%+v", events)
	}
	if !events[0].StartsAt.In(loc).Equal(time.Date(2026, 3, 8, 1, 30, 0, 0, loc)) {
		t.Fatalf("start=%s", events[0].StartsAt.In(loc))
	}
	nextStart := time.Date(2026, 3, 9, 0, 0, 0, 0, loc)
	next, err := Expand(body, loc, nextStart, nextStart.AddDate(0, 0, 1))
	if err != nil {
		t.Fatalf("Expand next civil day: %v", err)
	}
	if len(next) != 0 {
		t.Fatalf("spring-forward event leaked into next civil day: %+v", next)
	}
}

func TestExpandDSTFallBackKeepsLateEveningOnThatCivilDay(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 11, 1, 0, 0, 0, 0, loc)
	body := []byte(`BEGIN:VCALENDAR
VERSION:2.0
BEGIN:VEVENT
UID:late-fallback
DTSTART;TZID=America/New_York:20261101T233000
DTEND;TZID=America/New_York:20261101T234500
SUMMARY:Late
END:VEVENT
END:VCALENDAR
`)
	events, err := Expand(body, loc, day, day.AddDate(0, 0, 1))
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("23:30 on fall-back day must stay on Nov 1, events=%+v", events)
	}
	wrongEnd := day.Add(24 * time.Hour)
	if !wrongEnd.Before(day.AddDate(0, 0, 1)) {
		t.Fatal("test assumption: +24h is before next civil midnight on fall-back")
	}
	if events[0].StartsAt.In(loc).Hour() != 23 {
		t.Fatalf("start hour=%d, want 23", events[0].StartsAt.In(loc).Hour())
	}
	next := day.AddDate(0, 0, 1)
	leaked, err := Expand(body, loc, next, next.AddDate(0, 0, 1))
	if err != nil {
		t.Fatalf("Expand next: %v", err)
	}
	if len(leaked) != 0 {
		t.Fatalf("late evening leaked into Nov 2: %+v", leaked)
	}
}

func TestExpandAllDaySpanningDSTSpringForward(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`BEGIN:VCALENDAR
VERSION:2.0
BEGIN:VEVENT
UID:holiday
DTSTART;VALUE=DATE:20260308
DTEND;VALUE=DATE:20260310
SUMMARY:Long weekend
END:VEVENT
END:VCALENDAR
`)
	mar8 := time.Date(2026, 3, 8, 0, 0, 0, 0, loc)
	mar9 := time.Date(2026, 3, 9, 0, 0, 0, 0, loc)
	mar10 := time.Date(2026, 3, 10, 0, 0, 0, 0, loc)
	on8, err := Expand(body, loc, mar8, mar8.AddDate(0, 0, 1))
	if err != nil {
		t.Fatalf("Expand Mar 8: %v", err)
	}
	on9, err := Expand(body, loc, mar9, mar9.AddDate(0, 0, 1))
	if err != nil {
		t.Fatalf("Expand Mar 9: %v", err)
	}
	on10, err := Expand(body, loc, mar10, mar10.AddDate(0, 0, 1))
	if err != nil {
		t.Fatalf("Expand Mar 10: %v", err)
	}
	if len(on8) != 1 || len(on9) != 1 {
		t.Fatalf("all-day spanning DST should occupy Mar 8 and Mar 9, got 8=%+v 9=%+v", on8, on9)
	}
	if len(on10) != 0 {
		t.Fatalf("exclusive DATE DTEND leaked into Mar 10: %+v", on10)
	}
	if !on8[0].EndsAt.In(loc).Equal(mar10) {
		t.Fatalf("end=%s want %s", on8[0].EndsAt.In(loc), mar10)
	}
}

func TestExpandRecurringAllDayDoesNotLeakAcrossDST(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`BEGIN:VCALENDAR
VERSION:2.0
BEGIN:VEVENT
UID:daily-allday
DTSTART;VALUE=DATE:20260307
DTEND;VALUE=DATE:20260308
RRULE:FREQ=DAILY;COUNT=3
SUMMARY:Daily
END:VEVENT
END:VCALENDAR
`)
	mar8 := time.Date(2026, 3, 8, 0, 0, 0, 0, loc)
	mar9 := time.Date(2026, 3, 9, 0, 0, 0, 0, loc)
	on8, err := Expand(body, loc, mar8, mar8.AddDate(0, 0, 1))
	if err != nil {
		t.Fatalf("Expand Mar 8: %v", err)
	}
	if len(on8) != 1 || on8[0].StartsAt.In(loc).Day() != 8 {
		t.Fatalf("Mar 8=%+v", on8)
	}
	on9, err := Expand(body, loc, mar9, mar9.AddDate(0, 0, 1))
	if err != nil {
		t.Fatalf("Expand Mar 9: %v", err)
	}
	if len(on9) != 1 || on9[0].StartsAt.In(loc).Day() != 9 {
		t.Fatalf("Mar 9 must be the Mar 9 occurrence only, got %+v", on9)
	}
	if !on9[0].EndsAt.In(loc).Equal(mar9.AddDate(0, 0, 1)) {
		t.Fatalf("Mar 9 end=%s want next civil midnight", on9[0].EndsAt.In(loc))
	}
}

func TestExpandCancelledRecurrenceExceptionOmitsThatOccurrence(t *testing.T) {
	loc := time.UTC
	windowStart := time.Date(2026, 8, 17, 0, 0, 0, 0, loc)
	windowEnd := time.Date(2026, 8, 21, 0, 0, 0, 0, loc)
	body := []byte(`BEGIN:VCALENDAR
VERSION:2.0
BEGIN:VEVENT
UID:series
DTSTART:20260817T150000Z
DTEND:20260817T160000Z
RRULE:FREQ=DAILY;COUNT=3
SUMMARY:Standup
END:VEVENT
BEGIN:VEVENT
UID:series
RECURRENCE-ID:20260818T150000Z
DTSTART:20260818T150000Z
DTEND:20260818T160000Z
STATUS:CANCELLED
SUMMARY:Standup
END:VEVENT
END:VCALENDAR
`)
	events, err := Expand(body, loc, windowStart, windowEnd)
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	var days []int
	for _, ev := range events {
		days = append(days, ev.StartsAt.UTC().Day())
	}
	if len(days) != 2 || days[0] != 17 || days[1] != 19 {
		t.Fatalf("days=%v, want 17 and 19 only (18 cancelled via RECURRENCE-ID)", days)
	}
}

func TestExpandRDateAndExDate(t *testing.T) {
	loc := time.UTC
	windowStart := time.Date(2026, 8, 17, 0, 0, 0, 0, loc)
	windowEnd := time.Date(2026, 8, 22, 0, 0, 0, 0, loc)
	body := []byte(`BEGIN:VCALENDAR
VERSION:2.0
BEGIN:VEVENT
UID:series
DTSTART:20260817T150000Z
DTEND:20260817T160000Z
RRULE:FREQ=DAILY;COUNT=3
EXDATE:20260818T150000Z
RDATE:20260821T150000Z
SUMMARY:Standup
END:VEVENT
END:VCALENDAR
`)
	events, err := Expand(body, loc, windowStart, windowEnd)
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	var days []int
	for _, ev := range events {
		days = append(days, ev.StartsAt.UTC().Day())
	}
	if len(days) != 3 || days[0] != 17 || days[1] != 19 || days[2] != 21 {
		t.Fatalf("days=%v, want 17,19,21 (18 excluded, 21 via RDATE)", days)
	}
}

func TestExpandRDateWithoutRRule(t *testing.T) {
	loc := time.UTC
	windowStart := time.Date(2026, 8, 17, 0, 0, 0, 0, loc)
	windowEnd := time.Date(2026, 8, 21, 0, 0, 0, 0, loc)
	body := []byte(`BEGIN:VCALENDAR
VERSION:2.0
BEGIN:VEVENT
UID:rdates
DTSTART:20260817T150000Z
DTEND:20260817T160000Z
RDATE:20260818T150000Z,20260819T150000Z
SUMMARY:Pickup
END:VEVENT
END:VCALENDAR
`)
	events, err := Expand(body, loc, windowStart, windowEnd)
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	var days []int
	for _, ev := range events {
		days = append(days, ev.StartsAt.UTC().Day())
	}
	if len(days) != 3 || days[0] != 17 || days[1] != 18 || days[2] != 19 {
		t.Fatalf("days=%v, want 17,18,19 from DTSTART+RDATE without RRULE", days)
	}
}

func TestExpandExDateWithoutRRule(t *testing.T) {
	loc := time.UTC
	windowStart := time.Date(2026, 8, 17, 0, 0, 0, 0, loc)
	windowEnd := time.Date(2026, 8, 21, 0, 0, 0, 0, loc)
	body := []byte(`BEGIN:VCALENDAR
VERSION:2.0
BEGIN:VEVENT
UID:exdates
DTSTART:20260817T150000Z
DTEND:20260817T160000Z
RDATE:20260818T150000Z,20260819T150000Z
EXDATE:20260818T150000Z
SUMMARY:Pickup
END:VEVENT
END:VCALENDAR
`)
	events, err := Expand(body, loc, windowStart, windowEnd)
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	var days []int
	for _, ev := range events {
		days = append(days, ev.StartsAt.UTC().Day())
	}
	if len(days) != 2 || days[0] != 17 || days[1] != 19 {
		t.Fatalf("days=%v, want 17 and 19 (18 excluded without RRULE)", days)
	}
}

func TestExpandMovedOverrideIntoWindow(t *testing.T) {
	loc := time.UTC
	windowStart := time.Date(2026, 8, 17, 0, 0, 0, 0, loc)
	windowEnd := time.Date(2026, 8, 18, 0, 0, 0, 0, loc)
	body := []byte(`BEGIN:VCALENDAR
VERSION:2.0
BEGIN:VEVENT
UID:series
DTSTART:20260824T150000Z
DTEND:20260824T160000Z
RRULE:FREQ=DAILY;COUNT=3
SUMMARY:Standup
END:VEVENT
BEGIN:VEVENT
UID:series
RECURRENCE-ID:20260824T150000Z
DTSTART:20260817T150000Z
DTEND:20260817T160000Z
SUMMARY:Moved to today
END:VEVENT
END:VCALENDAR
`)
	events, err := Expand(body, loc, windowStart, windowEnd)
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if len(events) != 1 || events[0].Title != "Moved to today" {
		t.Fatalf("moved-in override missing: %+v", events)
	}
	if events[0].StartsAt.UTC().Day() != 17 {
		t.Fatalf("start=%s, want Aug 17", events[0].StartsAt)
	}
}

func TestExpandMovedOverrideOutOfWindow(t *testing.T) {
	loc := time.UTC
	windowStart := time.Date(2026, 8, 17, 0, 0, 0, 0, loc)
	windowEnd := time.Date(2026, 8, 20, 0, 0, 0, 0, loc)
	body := []byte(`BEGIN:VCALENDAR
VERSION:2.0
BEGIN:VEVENT
UID:series
DTSTART:20260817T150000Z
DTEND:20260817T160000Z
RRULE:FREQ=DAILY;COUNT=3
SUMMARY:Standup
END:VEVENT
BEGIN:VEVENT
UID:series
RECURRENCE-ID:20260817T150000Z
DTSTART:20260824T150000Z
DTEND:20260824T160000Z
SUMMARY:Moved out
END:VEVENT
END:VCALENDAR
`)
	events, err := Expand(body, loc, windowStart, windowEnd)
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	var days []int
	var titles []string
	for _, ev := range events {
		days = append(days, ev.StartsAt.UTC().Day())
		titles = append(titles, ev.Title)
	}
	if len(days) != 2 || days[0] != 18 || days[1] != 19 {
		t.Fatalf("days=%v, want 18,19 (17 moved outside window)", days)
	}
	for _, title := range titles {
		if title == "Moved out" {
			t.Fatalf("moved-out override leaked into window: %v", titles)
		}
	}
}

func TestExpandMovedOverrideInsideWindowAppearsOnce(t *testing.T) {
	loc := time.UTC
	windowStart := time.Date(2026, 8, 17, 0, 0, 0, 0, loc)
	windowEnd := time.Date(2026, 8, 20, 0, 0, 0, 0, loc)
	body := []byte(`BEGIN:VCALENDAR
VERSION:2.0
BEGIN:VEVENT
UID:series
DTSTART:20260817T150000Z
DTEND:20260817T160000Z
RRULE:FREQ=DAILY;COUNT=3
SUMMARY:Standup
END:VEVENT
BEGIN:VEVENT
UID:series
RECURRENCE-ID:20260818T150000Z
DTSTART:20260818T170000Z
DTEND:20260818T180000Z
SUMMARY:Standup moved
END:VEVENT
END:VCALENDAR
`)
	events, err := Expand(body, loc, windowStart, windowEnd)
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	moved := 0
	original18 := 0
	for _, ev := range events {
		if ev.Title == "Standup moved" {
			moved++
		}
		if ev.Title == "Standup" && ev.StartsAt.UTC().Day() == 18 && ev.StartsAt.UTC().Hour() == 15 {
			original18++
		}
	}
	if moved != 1 || original18 != 0 || len(events) != 3 {
		t.Fatalf("events=%+v, want one moved 18th plus 17 and 19", events)
	}
}

func TestExpandRejectsDenseSecondlyRecurrence(t *testing.T) {
	loc := time.UTC
	windowStart := time.Date(2026, 8, 17, 0, 0, 0, 0, loc)
	windowEnd := time.Date(2026, 8, 20, 0, 0, 0, 0, loc)
	body := []byte(`BEGIN:VCALENDAR
VERSION:2.0
BEGIN:VEVENT
UID:flood
DTSTART:20260817T000000Z
DTEND:20260817T000001Z
RRULE:FREQ=SECONDLY
SUMMARY:Flood
END:VEVENT
END:VCALENDAR
`)
	started := time.Now()
	_, err := Expand(body, loc, windowStart, windowEnd)
	elapsed := time.Since(started)
	if !errors.Is(err, ErrTooManyOccurrences) {
		t.Fatalf("err=%v, want ErrTooManyOccurrences", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("dense expansion took %s, likely unbounded", elapsed)
	}
}

func TestExpandRejectsOversizedBody(t *testing.T) {
	body := make([]byte, MaxBodyBytes+1)
	_, err := Expand(body, time.UTC, time.Now(), time.Now().Add(time.Hour))
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err=%v, want ErrTooLarge", err)
	}
}

func TestExpandAcceptsBodyAboveOldTwoMiBCeiling(t *testing.T) {
	const oldCeiling = 2 << 20
	loc := time.UTC
	day := time.Date(2026, 8, 17, 0, 0, 0, 0, loc)
	body := paddedICS(oldCeiling+1, "Pad")
	if len(body) <= oldCeiling || len(body) > MaxBodyBytes {
		t.Fatalf("padded size=%d, want (%d, %d]", len(body), oldCeiling, MaxBodyBytes)
	}
	events, err := Expand(body, loc, day, day.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if len(events) != 1 || events[0].Title != "Pad" {
		t.Fatalf("events=%+v", events)
	}
}

func paddedICS(minBytes int, summary string) []byte {
	prefix := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nBEGIN:VEVENT\r\nUID:pad\r\nDTSTART:20260817T150000Z\r\nDTEND:20260817T160000Z\r\nSUMMARY:" + summary + "\r\nDESCRIPTION:"
	suffix := "\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	n := minBytes - len(prefix) - len(suffix)
	if n < 1 {
		n = 1
	}
	return []byte(prefix + strings.Repeat("x", n) + suffix)
}
