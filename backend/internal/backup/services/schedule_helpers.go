package services

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"gorm.io/datatypes"
)

var allowedScheduleDays = map[string]struct{}{
	"MON": {},
	"TUE": {},
	"WED": {},
	"THU": {},
	"FRI": {},
	"SAT": {},
	"SUN": {},
}

func normalizeBackupType(value string) string {
	text := strings.ToUpper(strings.TrimSpace(value))
	if text == "" {
		return "FULL"
	}
	switch text {
	case "FULL", "SCHEMA", "DATA", "DIFF", "INCR":
		return text
	default:
		return ""
	}
}

func normalizeScheduleDays(days []string) ([]string, error) {
	if len(days) == 0 {
		return nil, nil
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(days))
	for _, item := range days {
		day := strings.ToUpper(strings.TrimSpace(item))
		if day == "" {
			continue
		}
		if _, ok := allowedScheduleDays[day]; !ok {
			return nil, fmt.Errorf("调度星期非法")
		}
		if _, ok := seen[day]; ok {
			continue
		}
		seen[day] = struct{}{}
		out = append(out, day)
	}
	if len(out) == 0 {
		return nil, nil
	}
	order := map[string]int{"MON": 1, "TUE": 2, "WED": 3, "THU": 4, "FRI": 5, "SAT": 6, "SUN": 7}
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if order[out[i]] > order[out[j]] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out, nil
}

func normalizeScheduleTime(value string) (string, error) {
	text := strings.TrimSpace(value)
	if text == "" {
		return "", nil
	}
	if _, err := time.Parse("15:04", text); err != nil {
		return "", fmt.Errorf("调度时间格式非法，应为 HH:MM")
	}
	return text, nil
}

func normalizeRetentionDays(value int) int {
	switch value {
	case 7, 30, 60, 90, 180, 365:
		return value
	default:
		return 30
	}
}

func marshalStringSliceOrNil(items []string) (datatypes.JSON, error) {
	if len(items) == 0 {
		return nil, nil
	}
	buf, err := json.Marshal(items)
	if err != nil {
		return nil, err
	}
	return datatypes.JSON(buf), nil
}

func parseScheduleDays(raw datatypes.JSON) []string {
	if len(raw) == 0 {
		return nil
	}
	var out []string
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	items, err := normalizeScheduleDays(out)
	if err != nil {
		return nil
	}
	return items
}

func mergeScheduleDays(current datatypes.JSON, next []string) ([]string, error) {
	if len(next) == 0 {
		return parseScheduleDays(current), nil
	}
	return normalizeScheduleDays(next)
}

func mergeScheduleTime(current string, next string) (string, error) {
	if strings.TrimSpace(next) == "" {
		return strings.TrimSpace(current), nil
	}
	return normalizeScheduleTime(next)
}

func calculateExpireAt(retentionDays int) *time.Time {
	days := normalizeRetentionDays(retentionDays)
	expireAt := time.Now().Add(time.Duration(days) * 24 * time.Hour)
	return &expireAt
}
