package main

import (
	"testing"
	"time"

	"github.com/damirmur/swisseph_build/pkg/astro"
)

func TestCalendarStoreRoundTrip(t *testing.T) {
	t.Setenv("CALENDAR_STORE_DIR", t.TempDir())
	f := &calendarYearFile{
		Year:       2027,
		Version:    calendarStoreVersion,
		ComputedAt: "2027-01-01T05:10:00Z",
		From:       "2026-12-31T00:00",
		To:         "2028-01-01T23:59",
		Events: []astro.CalendarEvent{
			{Date: "2027-01-01T00:00", Type: "lunD", Sign: intPtr(9)},
			{Date: "2027-01-02T03:04", Type: "exA", Planets: []string{"0", "1"}, Aspect: "Trine"},
		},
	}
	if err := saveCalendarYear(f); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := loadCalendarYear(2027)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got.Events) != 2 || got.Events[0].Type != "lunD" || got.Events[1].Aspect != "Trine" {
		t.Fatalf("события не совпали: %+v", got.Events)
	}

	// Устаревшая версия — повод пересчитать, а не отдавать старые минуты.
	f.Version = "calendar/v0"
	if err := saveCalendarYear(f); err != nil {
		t.Fatalf("save stale: %v", err)
	}
	if _, err := loadCalendarYear(2027); err == nil {
		t.Fatal("устаревшая версия должна давать ошибку")
	}
}

func TestFormatStoreTime(t *testing.T) {
	moscow, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		t.Skip("нет tzdata Europe/Moscow")
	}
	if got := formatStoreTime("2027-01-01T00:00", moscow); got != "2027-01-01T03:00" {
		t.Fatalf("Москва: %q", got)
	}
	if got := formatStoreTime("2027-01-01T00:00", time.UTC); got != "2027-01-01T00:00" {
		t.Fatalf("UTC: %q", got)
	}
	if got := formatStoreTime("", moscow); got != "" {
		t.Fatalf("пустая строка: %q", got)
	}
	if got := formatStoreTime("не дата", moscow); got != "не дата" {
		t.Fatalf("невалидная строка: %q", got)
	}
}

func TestYearsBetween(t *testing.T) {
	from := time.Date(2026, 12, 31, 23, 0, 0, 0, time.UTC)
	to := time.Date(2027, 1, 1, 1, 0, 0, 0, time.UTC)
	years := yearsBetween(from, to)
	if len(years) != 2 || years[0] != 2026 || years[1] != 2027 {
		t.Fatalf("годы на стыке: %v", years)
	}
	if got := yearsBetween(from, from); len(got) != 1 || got[0] != 2026 {
		t.Fatalf("один год: %v", got)
	}
}

func TestCalendarEventsForRange(t *testing.T) {
	t.Setenv("CALENDAR_STORE_DIR", t.TempDir())
	for _, f := range []*calendarYearFile{
		{
			Year: 2026, Version: calendarStoreVersion, From: "2025-12-31T00:00", To: "2027-01-01T23:59",
			Events: []astro.CalendarEvent{{Date: "2026-12-31T23:30", Type: "lunD"}},
		},
		{
			Year: 2027, Version: calendarStoreVersion, From: "2026-12-31T00:00", To: "2028-01-01T23:59",
			// Пересечение с 2026-м: тот же момент должен схлопнуться.
			Events: []astro.CalendarEvent{
				{Date: "2026-12-31T23:30", Type: "lunD"},
				{Date: "2027-01-01T00:30", Type: "lunD"},
			},
		},
	} {
		if err := saveCalendarYear(f); err != nil {
			t.Fatalf("save %d: %v", f.Year, err)
		}
	}
	from := time.Date(2026, 12, 31, 23, 0, 0, 0, time.UTC)
	to := time.Date(2027, 1, 1, 1, 0, 0, 0, time.UTC)
	events, err := calendarEventsForRange([]int{2026, 2027}, from, to)
	if err != nil {
		t.Fatalf("range: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("ожидалось 2 события без дублей, получено %d: %+v", len(events), events)
	}

	if _, err := calendarEventsForRange([]int{2030}, from, to); err == nil {
		t.Fatal("отсутствующий год должен давать ошибку (fallback)")
	}
}
