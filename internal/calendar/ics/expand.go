package ics

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	ical "github.com/arran4/golang-ical"
	"github.com/teambition/rrule-go"
)

const (
	MaxBodyBytes      = 32 << 20
	MaxOccurrenceScan = 100_000
	MaxExpandedEvents = 5_000
)

var (
	ErrTooLarge           = errors.New("calendar feed too large")
	ErrTooManyOccurrences = errors.New("calendar has too many recurring events to process")
	ErrUnknownTimezone    = errors.New("unsupported calendar timezone")
	ErrInvalidCalendar    = errors.New("invalid calendar data")
)

type Event struct {
	UID      string
	Title    string
	StartsAt time.Time
	EndsAt   time.Time
	AllDay   bool
	Location string
}

func Expand(body []byte, boardLoc *time.Location, windowStart, windowEnd time.Time) ([]Event, error) {
	if len(body) > MaxBodyBytes {
		return nil, ErrTooLarge
	}
	if boardLoc == nil {
		boardLoc = time.UTC
	}
	cal, err := ical.ParseCalendar(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidCalendar, err)
	}

	overrides := map[overrideKey]*ical.VEvent{}
	var masters []*ical.VEvent
	for _, event := range cal.Events() {
		recID := event.GetProperty(ical.ComponentPropertyRecurrenceId)
		hasRecID := recID != nil && strings.TrimSpace(recID.Value) != ""
		if isCancelled(event) && !hasRecID {
			continue
		}
		if err := validateEventTimezones(event); err != nil {
			return nil, err
		}
		if hasRecID {
			recAt, _, err := parseEventTime(recID, boardLoc, isDateValue(recID))
			if err != nil {
				return nil, err
			}
			uid := eventUID(event)
			overrides[overrideKey{UID: uid, StartUnix: recAt.UTC().Unix()}] = event
			continue
		}
		masters = append(masters, event)
	}

	consumed := map[overrideKey]struct{}{}
	out := make([]Event, 0)
	for _, event := range masters {
		instances, err := expandEvent(event, boardLoc, windowStart, windowEnd, overrides, consumed)
		if err != nil {
			return nil, err
		}
		out = append(out, instances...)
		if len(out) > MaxExpandedEvents {
			return nil, ErrTooManyOccurrences
		}
	}
	for key, override := range overrides {
		if _, seen := consumed[key]; seen {
			continue
		}
		if isCancelled(override) {
			continue
		}
		inst, err := occurrenceFromEvent(override, boardLoc, key.UID)
		if err != nil {
			return nil, err
		}
		if !overlaps(inst.StartsAt, inst.EndsAt, windowStart, windowEnd) {
			continue
		}
		out = append(out, inst)
		if len(out) > MaxExpandedEvents {
			return nil, ErrTooManyOccurrences
		}
	}
	return out, nil
}

type overrideKey struct {
	UID       string
	StartUnix int64
}

func isCancelled(event *ical.VEvent) bool {
	prop := event.GetProperty(ical.ComponentPropertyStatus)
	return prop != nil && strings.EqualFold(strings.TrimSpace(prop.Value), "CANCELLED")
}

func eventUID(event *ical.VEvent) string {
	prop := event.GetProperty(ical.ComponentPropertyUniqueId)
	if prop == nil {
		return ""
	}
	return strings.TrimSpace(prop.Value)
}

func eventTitle(event *ical.VEvent) string {
	prop := event.GetProperty(ical.ComponentPropertySummary)
	if prop == nil {
		return ""
	}
	return strings.TrimSpace(prop.Value)
}

func eventLocation(event *ical.VEvent) string {
	prop := event.GetProperty(ical.ComponentPropertyLocation)
	if prop == nil {
		return ""
	}
	return strings.TrimSpace(prop.Value)
}

func validateEventTimezones(event *ical.VEvent) error {
	for _, propName := range []ical.ComponentProperty{
		ical.ComponentPropertyDtStart,
		ical.ComponentPropertyDtEnd,
		ical.ComponentPropertyRecurrenceId,
		ical.ComponentPropertyExdate,
		ical.ComponentPropertyRdate,
	} {
		for _, prop := range event.GetProperties(propName) {
			if err := validateTZID(prop.ICalParameters); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateTZID(params map[string][]string) error {
	tzids := params["TZID"]
	if len(tzids) == 0 {
		return nil
	}
	if _, err := time.LoadLocation(tzids[0]); err != nil {
		return ErrUnknownTimezone
	}
	return nil
}

func expandEvent(event *ical.VEvent, boardLoc *time.Location, windowStart, windowEnd time.Time, overrides map[overrideKey]*ical.VEvent, consumed map[overrideKey]struct{}) ([]Event, error) {
	startProp := event.GetProperty(ical.ComponentPropertyDtStart)
	if startProp == nil || strings.TrimSpace(startProp.Value) == "" {
		return nil, nil
	}
	start, allDay, err := parseEventTime(startProp, boardLoc, isDateValue(startProp))
	if err != nil {
		return nil, err
	}
	end, err := eventEnd(event, start, allDay, boardLoc)
	if err != nil {
		return nil, err
	}
	duration := end.Sub(start)
	if duration < 0 {
		duration = 0
	}
	allDayDays := 0
	if allDay {
		allDayDays = civilDaysBetween(start, end)
		if allDayDays < 1 {
			allDayDays = 1
		}
	}

	uid := eventUID(event)
	title := eventTitle(event)
	location := eventLocation(event)

	rDates, err := parseMultiTimes(event, ical.ComponentPropertyRdate, boardLoc, allDay)
	if err != nil {
		return nil, err
	}
	exDates, err := parseMultiTimes(event, ical.ComponentPropertyExdate, boardLoc, allDay)
	if err != nil {
		return nil, err
	}
	rruleProps := event.GetProperties(ical.ComponentPropertyRrule)
	occurrences := []time.Time{start}
	if len(rruleProps) > 0 || len(rDates) > 0 || len(exDates) > 0 {
		set := &rrule.Set{}
		if len(rruleProps) > 0 {
			for _, prop := range rruleProps {
				rr, err := rrule.StrToRRule(strings.TrimSpace(prop.Value))
				if err != nil {
					return nil, fmt.Errorf("%w: %v", ErrInvalidCalendar, err)
				}
				rr.DTStart(start)
				set.RRule(rr)
			}
		} else {
			set.RDate(start)
		}
		for _, rd := range rDates {
			set.RDate(rd)
		}
		for _, ex := range exDates {
			set.ExDate(ex)
		}
		searchStart := windowStart.Add(-duration)
		if allDay {
			searchStart = windowStart.AddDate(0, 0, -allDayDays)
		}
		occurrences, err = collectOccurrences(set, searchStart, windowEnd)
		if err != nil {
			return nil, err
		}
	}

	out := make([]Event, 0, len(occurrences))
	for _, occStart := range occurrences {
		key := overrideKey{UID: uid, StartUnix: occStart.UTC().Unix()}
		if override, ok := overrides[key]; ok {
			consumed[key] = struct{}{}
			if isCancelled(override) {
				continue
			}
			inst, err := occurrenceFromEvent(override, boardLoc, uid)
			if err != nil {
				return nil, err
			}
			if overlaps(inst.StartsAt, inst.EndsAt, windowStart, windowEnd) {
				out = append(out, inst)
			}
			continue
		}
		occEnd := occStart.Add(duration)
		if allDay {
			occEnd = occStart.AddDate(0, 0, allDayDays)
		}
		if !overlaps(occStart, occEnd, windowStart, windowEnd) {
			continue
		}
		out = append(out, Event{
			UID:      uid,
			Title:    title,
			StartsAt: occStart.UTC(),
			EndsAt:   occEnd.UTC(),
			AllDay:   allDay,
			Location: location,
		})
	}
	return out, nil
}

func collectOccurrences(set *rrule.Set, searchStart, windowEnd time.Time) ([]time.Time, error) {
	next := set.Iterator()
	out := make([]time.Time, 0)
	scanned := 0
	for {
		occ, ok := next()
		if !ok {
			return out, nil
		}
		scanned++
		if scanned > MaxOccurrenceScan {
			return nil, ErrTooManyOccurrences
		}
		if occ.After(windowEnd) {
			return out, nil
		}
		if occ.Before(searchStart) {
			continue
		}
		out = append(out, occ)
		if len(out) > MaxExpandedEvents {
			return nil, ErrTooManyOccurrences
		}
	}
}

func occurrenceFromEvent(event *ical.VEvent, boardLoc *time.Location, fallbackUID string) (Event, error) {
	startProp := event.GetProperty(ical.ComponentPropertyDtStart)
	if startProp == nil {
		return Event{}, fmt.Errorf("%w: missing DTSTART", ErrInvalidCalendar)
	}
	start, allDay, err := parseEventTime(startProp, boardLoc, isDateValue(startProp))
	if err != nil {
		return Event{}, err
	}
	end, err := eventEnd(event, start, allDay, boardLoc)
	if err != nil {
		return Event{}, err
	}
	uid := eventUID(event)
	if uid == "" {
		uid = fallbackUID
	}
	return Event{
		UID:      uid,
		Title:    eventTitle(event),
		StartsAt: start.UTC(),
		EndsAt:   end.UTC(),
		AllDay:   allDay,
		Location: eventLocation(event),
	}, nil
}

func eventEnd(event *ical.VEvent, start time.Time, allDay bool, boardLoc *time.Location) (time.Time, error) {
	endProp := event.GetProperty(ical.ComponentPropertyDtEnd)
	if endProp != nil && strings.TrimSpace(endProp.Value) != "" {
		end, _, err := parseEventTime(endProp, boardLoc, isDateValue(endProp) || allDay)
		if err != nil {
			return time.Time{}, err
		}
		return end, nil
	}
	durProp := event.GetProperty(ical.ComponentPropertyDuration)
	if allDay {
		days := 1
		if durProp != nil && strings.TrimSpace(durProp.Value) != "" {
			d, err := parseICSDuration(durProp.Value)
			if err != nil {
				return time.Time{}, fmt.Errorf("%w: %v", ErrInvalidCalendar, err)
			}
			days = int(d / (24 * time.Hour))
			if days < 1 {
				days = 1
			}
		}
		return start.AddDate(0, 0, days), nil
	}
	if durProp != nil && strings.TrimSpace(durProp.Value) != "" {
		d, err := parseICSDuration(durProp.Value)
		if err != nil {
			return time.Time{}, fmt.Errorf("%w: %v", ErrInvalidCalendar, err)
		}
		return start.Add(d), nil
	}
	return start.Add(time.Hour), nil
}

func civilDaysBetween(start, end time.Time) int {
	loc := start.Location()
	if loc == nil {
		loc = time.UTC
	}
	a := time.Date(start.In(loc).Year(), start.In(loc).Month(), start.In(loc).Day(), 0, 0, 0, 0, loc)
	endLocal := end.In(loc)
	b := time.Date(endLocal.Year(), endLocal.Month(), endLocal.Day(), 0, 0, 0, 0, loc)
	days := 0
	for day := a; day.Before(b); day = day.AddDate(0, 0, 1) {
		days++
		if days > 3660 {
			break
		}
	}
	return days
}

func parseICSDuration(raw string) (time.Duration, error) {
	value := strings.TrimSpace(raw)
	sign := time.Duration(1)
	if strings.HasPrefix(value, "-") {
		sign = -1
		value = strings.TrimPrefix(value, "-")
	} else {
		value = strings.TrimPrefix(value, "+")
	}
	if !strings.HasPrefix(value, "P") {
		return 0, fmt.Errorf("invalid duration")
	}
	value = strings.TrimPrefix(value, "P")
	if value == "" {
		return 0, fmt.Errorf("invalid duration")
	}
	var total time.Duration
	if strings.HasSuffix(value, "W") && !strings.Contains(value, "T") {
		weeks, err := strconv.Atoi(strings.TrimSuffix(value, "W"))
		if err != nil || weeks < 0 {
			return 0, fmt.Errorf("invalid duration")
		}
		return sign * time.Duration(weeks) * 7 * 24 * time.Hour, nil
	}
	datePart, timePart, foundT := strings.Cut(value, "T")
	if err := addICSDurationUnits(datePart, map[byte]time.Duration{
		'D': 24 * time.Hour,
	}, &total); err != nil {
		return 0, err
	}
	if foundT {
		if err := addICSDurationUnits(timePart, map[byte]time.Duration{
			'H': time.Hour,
			'M': time.Minute,
			'S': time.Second,
		}, &total); err != nil {
			return 0, err
		}
	}
	return sign * total, nil
}

func addICSDurationUnits(part string, units map[byte]time.Duration, total *time.Duration) error {
	if part == "" {
		return nil
	}
	num := 0
	sawDigit := false
	for i := 0; i < len(part); i++ {
		ch := part[i]
		if ch >= '0' && ch <= '9' {
			sawDigit = true
			num = num*10 + int(ch-'0')
			continue
		}
		unit, ok := units[ch]
		if !ok || !sawDigit {
			return fmt.Errorf("invalid duration")
		}
		*total += time.Duration(num) * unit
		num = 0
		sawDigit = false
	}
	if sawDigit {
		return fmt.Errorf("invalid duration")
	}
	return nil
}

func parseMultiTimes(event *ical.VEvent, prop ical.ComponentProperty, boardLoc *time.Location, allDay bool) ([]time.Time, error) {
	props := event.GetProperties(prop)
	out := make([]time.Time, 0)
	for _, p := range props {
		for _, raw := range strings.Split(p.Value, ",") {
			raw = strings.TrimSpace(raw)
			if raw == "" {
				continue
			}
			clone := *p
			clone.Value = raw
			t, _, err := parseEventTime(&clone, boardLoc, allDay || isDateValue(&clone))
			if err != nil {
				return nil, err
			}
			out = append(out, t)
		}
	}
	return out, nil
}

func isDateValue(prop *ical.IANAProperty) bool {
	if prop == nil {
		return false
	}
	values := prop.ICalParameters["VALUE"]
	if len(values) == 1 && strings.EqualFold(values[0], "DATE") {
		return true
	}
	return !strings.Contains(prop.Value, "T")
}

func parseEventTime(prop *ical.IANAProperty, boardLoc *time.Location, allDay bool) (time.Time, bool, error) {
	if err := validateTZID(prop.ICalParameters); err != nil {
		return time.Time{}, false, err
	}
	value := strings.TrimSpace(prop.Value)
	loc := boardLoc
	if tzids := prop.ICalParameters["TZID"]; len(tzids) == 1 {
		loaded, err := time.LoadLocation(tzids[0])
		if err != nil {
			return time.Time{}, false, ErrUnknownTimezone
		}
		loc = loaded
	}
	if strings.HasSuffix(value, "Z") {
		loc = time.UTC
		value = strings.TrimSuffix(value, "Z")
		if allDay || !strings.Contains(value, "T") {
			t, err := time.ParseInLocation("20060102", value, time.UTC)
			if err != nil {
				return time.Time{}, false, fmt.Errorf("%w: %v", ErrInvalidCalendar, err)
			}
			return t, true, nil
		}
		t, err := time.ParseInLocation("20060102T150405", value, time.UTC)
		if err != nil {
			return time.Time{}, false, fmt.Errorf("%w: %v", ErrInvalidCalendar, err)
		}
		return t, false, nil
	}
	if allDay || !strings.Contains(value, "T") {
		t, err := time.ParseInLocation("20060102", value, loc)
		if err != nil {
			return time.Time{}, false, fmt.Errorf("%w: %v", ErrInvalidCalendar, err)
		}
		return t, true, nil
	}
	t, err := time.ParseInLocation("20060102T150405", value, loc)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("%w: %v", ErrInvalidCalendar, err)
	}
	return t, false, nil
}

func overlaps(start, end, windowStart, windowEnd time.Time) bool {
	if !end.After(start) {
		end = start.Add(time.Nanosecond)
	}
	return start.Before(windowEnd) && end.After(windowStart)
}
