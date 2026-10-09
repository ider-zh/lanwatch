package api

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/ider-zh/lanwatch/internal/store"
)

type calendarSegment struct {
	Start int64  `json:"start"`
	End   int64  `json:"end"`
	Kind  string `json:"kind"`
}

type calendarDay struct {
	Date            string            `json:"date"`
	Start           int64             `json:"start"`
	End             int64             `json:"end"`
	Segments        []calendarSegment `json:"segments"`
	OnlineSeconds   int64             `json:"online_seconds"`
	SmoothedSeconds int64             `json:"smoothed_seconds"`
	UnknownSeconds  int64             `json:"unknown_seconds"`
	LongestSeconds  int64             `json:"longest_seconds"`
	MergedGaps      int               `json:"merged_gaps"`
}

var calendarZone = time.FixedZone("Asia/Shanghai", 8*3600)

func calendarSegments(sessions, coverage []store.CalendarSpan, from, to, firstSeen, smooth int64) []calendarSegment {
	type boundary struct {
		at              int64
		online, covered int
	}
	points := []boundary{{at: from}, {at: to}}
	for _, span := range sessions {
		start, end := max(from, span.Start), min(to, span.End)
		if start < end {
			points = append(points, boundary{at: start, online: 1}, boundary{at: end, online: -1})
		}
	}
	for _, span := range coverage {
		start, end := max(from, firstSeen, span.Start), min(to, span.End)
		if start < end {
			points = append(points, boundary{at: start, covered: 1}, boundary{at: end, covered: -1})
		}
	}
	sort.Slice(points, func(left, right int) bool { return points[left].at < points[right].at })
	result := make([]calendarSegment, 0)
	previous, online, covered := from, 0, 0
	for _, point := range points {
		if point.at > previous {
			kind := "unknown"
			if covered > 0 {
				kind = "offline"
				if online > 0 {
					kind = "online"
				}
			}
			if len(result) > 0 && result[len(result)-1].Kind == kind {
				result[len(result)-1].End = point.at
			} else {
				result = append(result, calendarSegment{Start: previous, End: point.at, Kind: kind})
			}
		}
		online += point.online
		covered += point.covered
		previous = point.at
	}
	for index := 1; index+1 < len(result); index++ {
		segment := &result[index]
		if segment.Kind == "offline" && segment.End-segment.Start <= smooth && result[index-1].Kind == "online" && result[index+1].Kind == "online" {
			segment.Kind = "merged"
		}
	}
	return result
}

func calendarDays(segments []calendarSegment, start time.Time, count int, now int64) []calendarDay {
	days := make([]calendarDay, 0, count)
	for offset := 0; offset < count; offset++ {
		date := start.AddDate(0, 0, offset)
		day := calendarDay{Date: date.Format("2006-01-02"), Start: date.Unix(), End: date.AddDate(0, 0, 1).Unix(), Segments: []calendarSegment{}}
		var streak int64
		for _, segment := range segments {
			part := calendarSegment{Start: max(day.Start, segment.Start), End: min(day.End, segment.End, now), Kind: segment.Kind}
			if part.Start >= part.End {
				continue
			}
			day.Segments = append(day.Segments, part)
			duration := part.End - part.Start
			if part.Kind == "online" {
				day.OnlineSeconds += duration
			}
			if part.Kind == "merged" {
				day.MergedGaps++
			}
			if part.Kind == "unknown" {
				day.UnknownSeconds += duration
			}
			if part.Kind == "online" || part.Kind == "merged" {
				day.SmoothedSeconds += duration
				streak += duration
				day.LongestSeconds = max(day.LongestSeconds, streak)
			} else {
				streak = 0
			}
		}
		days = append(days, day)
	}
	return days
}

func (a *API) handleDeviceCalendar(w http.ResponseWriter, r *http.Request) {
	mac := strings.ToUpper(chi.URLParam(r, "mac"))
	device, err := a.st.GetDevice(mac)
	if err != nil {
		writeError(w, 500, "internal error")
		return
	}
	if device == nil {
		writeError(w, 404, "device not found")
		return
	}
	count := 7
	if value := r.URL.Query().Get("days"); value != "" {
		count, err = strconv.Atoi(value)
		if err != nil || (count != 7 && count != 30) {
			writeError(w, 400, "days must be 7 or 30")
			return
		}
	}
	smooth := int64(300)
	if value := r.URL.Query().Get("smooth"); value != "" {
		smooth, err = strconv.ParseInt(value, 10, 64)
		if err != nil || (smooth != 0 && smooth != 300 && smooth != 600) {
			writeError(w, 400, "smooth must be 0, 300 or 600")
			return
		}
	}
	now := time.Now()
	endDate, _ := time.ParseInLocation("2006-01-02", now.In(calendarZone).Format("2006-01-02"), calendarZone)
	if value := r.URL.Query().Get("end_date"); value != "" {
		endDate, err = time.ParseInLocation("2006-01-02", value, calendarZone)
		if err != nil || endDate.After(now) {
			writeError(w, 400, "end_date must be a valid date not in the future")
			return
		}
	}
	start := endDate.AddDate(0, 0, 1-count)
	end := min(endDate.AddDate(0, 0, 1).Unix(), now.Unix())
	interval := max(int64(a.cfg.Collect.Interval.Value().Seconds()), 1)
	sessions, err := a.st.CalendarSessions(mac, start.Unix()-600, min(end+600, now.Unix()))
	if err != nil {
		writeError(w, 500, "load calendar sessions failed")
		return
	}
	coverage, err := a.st.CalendarCoverage(start.Unix()-600, min(end+600, now.Unix()), interval)
	if err != nil {
		writeError(w, 500, "load calendar coverage failed")
		return
	}
	var rawSince int64
	if err := a.st.QueryRow(`SELECT raw_since FROM calendar_metadata LIMIT 1`).Scan(&rawSince); err != nil {
		writeError(w, 500, "load calendar metadata failed")
		return
	}
	segments := calendarSegments(sessions, coverage, start.Unix()-600, min(end+600, now.Unix()), device.FirstSeen, smooth)
	writeJSON(w, 200, map[string]any{"device": device, "days": calendarDays(segments, start, count, now.Unix()), "now": now.Unix(), "timezone": "Asia/Shanghai", "raw_since": rawSince, "smooth": smooth})
}
