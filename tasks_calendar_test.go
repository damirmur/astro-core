package main

import (
	"strings"
	"testing"
	"time"

	"github.com/damirmur/swisseph_build/pkg/astro"
)

func intPtr(v int) *int { return &v }

func testMSK(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(dailyDefaultTZ)
	if err != nil {
		t.Fatalf("Europe/Moscow: %v", err)
	}
	return loc
}

func testDailyEvents() []astro.CompactEvent {
	return []astro.CompactEvent{
		{Date: "2026-11-04T08:33", Type: "lunD", Sign: intPtr(26)},
		{Date: "2026-11-04T10:33", Type: "exA", Planets: []string{"1", "0"}, Aspect: "60", Degrees: []int{162, 221}},
		{Date: "2026-11-04T11:57", Type: "noC", Planets: []string{"1"}, VoidEnd: "2026-11-05T23:17"},
		{Date: "2026-11-04T15:31", Type: "exA", Planets: []string{"3", "5"}, Aspect: "60", Degrees: []int{204, 144}},
		{Date: "2026-11-04T19:24", Type: "exA", Planets: []string{"0", "2"}, Aspect: "0", Degrees: []int{221, 222}},
		{Date: "2026-11-04T21:00", Type: "chS", Planets: []string{"2"}, Sign: intPtr(7)},
		{Date: "2026-11-04T22:00", Type: "r", Planets: []string{"2"}, Retro: intPtr(1)},
	}
}

func TestDailyText(t *testing.T) {
	loc := testMSK(t)
	text := dailyText("2026-11-04", testDailyEvents(), loc, false)
	for _, want := range []string{
		"4\nСреда\n26 л.д., IV четверть ☾",
		"08:33 → 26 л.д.",
		"10:33 ⬤☾(12♍\uFE0E) ⚹ ☉(11♏\uFE0E)",
		"11:57 -☾ 23:17 05.11",
		"15:31 ⬤♀(24♎\uFE0E) ⚹ ♃(24♌\uFE0E)",
		"19:24 ⬤☉(11♏\uFE0E) ☌ ☿(12♏\uFE0E)",
		"21:00 ☿ → ♏\uFE0E",
		"22:00 ☿ R",
		"Время (мск)",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("в тексте дня нет %q:\n%s", want, text)
		}
	}
}

func TestDailyTextEN(t *testing.T) {
	loc := testMSK(t)
	text := dailyText("2026-11-04", testDailyEvents(), loc, true)
	for _, want := range []string{"4\nWednesday", "26 l.d., fourth quarter ☾", "Time (MSK)"} {
		if !strings.Contains(text, want) {
			t.Errorf("в EN-тексте нет %q:\n%s", want, text)
		}
	}
}

func TestDailyTransits(t *testing.T) {
	loc := testMSK(t)
	got := dailyTransits(testDailyEvents(), loc)
	if len(got) != 3 {
		t.Fatalf("транзитов %d, ожидалось 3: %+v", len(got), got)
	}
	first := got[0]
	if first.At != "2026-11-04T10:33:00+03:00" || first.P1 != "1" || first.P2 != "0" || first.Aspect != 60 {
		t.Fatalf("первый транзит неверен: %+v", first)
	}
	if len(first.De) != 2 || first.De[0] != 162 || first.De[1] != 221 {
		t.Fatalf("долготы транзита неверны: %+v", first.De)
	}
}

func TestDailyUTCOffset(t *testing.T) {
	loc := testMSK(t)
	if got := dailyUTCOffset("2026-11-04", loc); got != 3 {
		t.Fatalf("смещение Москвы = %v, ожидалось 3", got)
	}
}

func TestDailyTextEmptyEvents(t *testing.T) {
	loc := testMSK(t)
	text := dailyText("2026-11-04", nil, loc, false)
	if !strings.Contains(text, "4\nСреда") || !strings.Contains(text, "Время (мск)") {
		t.Fatalf("пустой день потерял шапку/подпись:\n%s", text)
	}
}
