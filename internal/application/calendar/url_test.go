package calendar

import "testing"

func TestCalendarHostKindExactHostsAndSpoofs(t *testing.T) {
	t.Parallel()
	cases := []struct {
		url  string
		want CalendarHostKind
	}{
		{"https://calendar.google.com/calendar/ical/family/private-token/basic.ics", CalendarHostKindGoogle},
		{"https://google.com/calendar/ical/x/basic.ics", CalendarHostKindGoogle},
		{"https://www.google.com/calendar/ical/x/basic.ics", CalendarHostKindGoogle},
		{"https://www.google.com/calendar/feeds/x/basic.ics", CalendarHostKindOther},
		{"https://google.com/calendar/ical", CalendarHostKindOther},
		{"https://icloud.com/published/2/guid", CalendarHostKindApple},
		{"https://p12-caldav.icloud.com/published/2/guid", CalendarHostKindApple},
		{"https://calendar.example.com/private/token.ics", CalendarHostKindOther},
		{"https://evil-google.com/calendar/ical/x/basic.ics", CalendarHostKindOther},
		{"https://google.com.evil.example/calendar/ical/x/basic.ics", CalendarHostKindOther},
		{"https://calendar.google.com.evil.example/calendar/ical/x/basic.ics", CalendarHostKindOther},
		{"https://evil-icloud.com/published/2/guid", CalendarHostKindOther},
		{"https://icloud.com.evil.example/published/2/guid", CalendarHostKindOther},
	}
	for _, tc := range cases {
		got := calendarHostKind(tc.url)
		if got != tc.want {
			t.Errorf("calendarHostKind(%q)=%q, want %q", tc.url, got, tc.want)
		}
	}
}

func TestCalendarHostKindUsesCanonicalHostCasing(t *testing.T) {
	t.Parallel()
	canonical, err := canonicalCalendarURL("https://CALENDAR.GOOGLE.COM/calendar/ical/X/basic.ics", false)
	if err != nil {
		t.Fatalf("canonicalCalendarURL: %v", err)
	}
	if got := calendarHostKind(canonical); got != CalendarHostKindGoogle {
		t.Fatalf("calendarHostKind(%q)=%q, want google", canonical, got)
	}
}
