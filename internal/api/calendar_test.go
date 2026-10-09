package api

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/ider-zh/lanwatch/internal/store"
)

func TestCalendarSmoothingAndDayBoundaries(t *testing.T) {
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, calendarZone)
	base := start.Unix()
	sessions := []store.CalendarSpan{{Start: base + 23*3600, End: base + 86400 - 120}, {Start: base + 86400 + 120, End: base + 86400 + 3600}}
	coverage := []store.CalendarSpan{{Start: base, End: base + 2*86400}}
	for _, smooth := range []int64{0, 300, 600} {
		segments := calendarSegments(sessions, coverage, base, base+2*86400, base, smooth)
		days := calendarDays(segments, start, 2, base+2*86400)
		for _, day := range days {
			if day.OnlineSeconds != 3480 {
				t.Fatalf("wrong daily raw duration: %+v", day)
			}
			want := int64(3480)
			if smooth > 0 {
				want = 3600
			}
			if day.SmoothedSeconds != want || day.LongestSeconds != want {
				t.Fatalf("wrong smoothed duration: %+v", day)
			}
			for _, segment := range day.Segments {
				if segment.Start < day.Start || segment.End > day.End {
					t.Fatalf("not clipped to day: %+v", segment)
				}
			}
		}
		if days[0].Date != "2026-10-01" || days[1].Date != "2026-10-02" {
			t.Fatal("wrong timezone boundaries")
		}
	}
}

func TestCalendarUnknownAndTrailingOfflineAreNotSmoothed(t *testing.T) {
	sessions := []store.CalendarSpan{{Start: 100, End: 200}, {Start: 400, End: 500}}
	for _, coverage := range [][]store.CalendarSpan{
		{{Start: 100, End: 600}},
		{{Start: 100, End: 200}, {Start: 400, End: 600}},
	} {
		segments := calendarSegments(sessions, coverage, 0, 600, 100, 600)
		if segments[0].Kind != "unknown" || segments[0].End != 100 {
			t.Fatal("pre-discovery time must be unknown")
		}
		if segments[len(segments)-1].Kind != "offline" {
			t.Fatal("trailing offline must remain pending")
		}
		want := "merged"
		if len(coverage) > 1 {
			want = "unknown"
		}
		if segments[2].Kind != want {
			t.Fatalf("gap kind = %s, want %s", segments[2].Kind, want)
		}
	}
}

func TestCalendarCurrentDayAndOverlappingSessions(t *testing.T) {
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, calendarZone)
	base := start.Unix()
	now := base + 7200
	segments := calendarSegments([]store.CalendarSpan{{Start: base, End: now}, {Start: base + 100, End: now}}, []store.CalendarSpan{{Start: base, End: now}}, base, now, base, 300)
	days := calendarDays(segments, start, 1, now)
	if days[0].OnlineSeconds != 7200 || len(days[0].Segments) != 1 || days[0].Segments[0].End != now {
		t.Fatalf("overcounted overlapping/future data: %+v", days)
	}
}

func TestCalendarGapThreshold(t *testing.T) {
	for _, gap := range []int64{300, 301, 600, 601} {
		for _, smooth := range []int64{0, 300, 600} {
			segments := calendarSegments([]store.CalendarSpan{{Start: 0, End: 100}, {Start: 100 + gap, End: 1000}}, []store.CalendarSpan{{Start: 0, End: 1000}}, 0, 1000, 0, smooth)
			if (segments[1].Kind == "merged") != (gap <= smooth) {
				t.Fatalf("gap=%d smooth=%d: %+v", gap, smooth, segments)
			}
		}
	}
}

func TestWatchAndCalendarAPI(t *testing.T) {
	api, cleanup := newTestAPI(t)
	defer cleanup()
	const mac = "AA-BB-CC-DD-EE-FF"
	if err := api.st.UpsertDevice(store.Device{MAC: mac, FirstSeen: 1, LastSeen: 1}); err != nil {
		t.Fatal(err)
	}
	response := doJSON(api, http.MethodPatch, "/api/devices/"+mac, map[string]any{"watched": true, "name": "Office"}, api.handlePatchDevice, map[string]string{"mac": mac})
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	if err := api.st.UpsertDevice(store.Device{MAC: mac, CurrentIP: "192.168.1.2"}); err != nil {
		t.Fatal(err)
	}
	response = doJSON(api, http.MethodGet, "/api/devices?watched=true", nil, api.handleDevices)
	var devices struct{ Devices []store.Device }
	if err := json.Unmarshal(response.Body.Bytes(), &devices); err != nil {
		t.Fatal(err)
	}
	if len(devices.Devices) != 1 || !devices.Devices[0].Watched || devices.Devices[0].FixedServer || devices.Devices[0].Name != "Office" {
		t.Fatalf("watch/name not preserved: %+v", devices)
	}
	response = doJSON(api, http.MethodGet, "/api/devices/"+mac+"/calendar?days=30&end_date=2026-01-31&smooth=600", nil, api.handleDeviceCalendar, map[string]string{"mac": mac})
	var calendar struct {
		Days     []calendarDay
		Timezone string
	}
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	if err := json.Unmarshal(response.Body.Bytes(), &calendar); err != nil {
		t.Fatal(err)
	}
	if len(calendar.Days) != 30 || calendar.Timezone != "Asia/Shanghai" || calendar.Days[0].UnknownSeconds != 86400 {
		t.Fatalf("bad empty calendar: %+v", calendar)
	}
	for _, query := range []string{"days=365", "days=bad", "smooth=-1", "smooth=bad", "end_date=bad", "end_date=2999-01-01"} {
		response = doJSON(api, http.MethodGet, "/api/devices/"+mac+"/calendar?"+query, nil, api.handleDeviceCalendar, map[string]string{"mac": mac})
		if response.Code != 400 {
			t.Fatalf("accepted bad query %s: %d", query, response.Code)
		}
	}
	response = doJSON(api, http.MethodPatch, "/api/devices/"+mac, map[string]any{"watched": false}, api.handlePatchDevice, map[string]string{"mac": mac})
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	response = doJSON(api, http.MethodGet, "/api/devices?watched=true", nil, api.handleDevices)
	if err := json.Unmarshal(response.Body.Bytes(), &devices); err != nil {
		t.Fatal(err)
	}
	if len(devices.Devices) != 0 {
		t.Fatal("unwatch failed")
	}
}
