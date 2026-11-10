package model

import (
	"errors"
	"strings"
	"unicode/utf8"
)

type RecordingLimits struct {
	MaxAgeDays  int   `json:"max_age_days"`
	MaxSegments int64 `json:"max_segments"`
	MaxBytes    int64 `json:"max_bytes"`
}
type CameraGroup struct {
	ID     string          `json:"id"`
	Name   string          `json:"name"`
	Limits RecordingLimits `json:"limits"`
}
type RecordingSettings struct {
	Revision int64           `json:"revision"`
	Global   RecordingLimits `json:"global"`
	Groups   []CameraGroup   `json:"groups"`
}

func (l RecordingLimits) Validate() error {
	if l.MaxAgeDays < 0 || l.MaxAgeDays > 3650 || l.MaxSegments < 0 || l.MaxSegments > 100000000 || l.MaxBytes < 0 || l.MaxBytes > 1<<50 {
		return errors.New("invalid recording limits")
	}
	return nil
}
func (s RecordingSettings) Validate() error {
	if s.Revision < 0 || len(s.Groups) > 128 {
		return errors.New("invalid recording settings")
	}
	if e := s.Global.Validate(); e != nil {
		return e
	}
	seen := map[string]bool{}
	names := map[string]bool{}
	for _, g := range s.Groups {
		key := strings.ToLower(strings.TrimSpace(g.Name))
		if !ValidID(g.ID) || seen[g.ID] || key == "" || names[key] || utf8.RuneCountInString(g.Name) > 100 {
			return errors.New("invalid camera group")
		}
		if e := g.Limits.Validate(); e != nil {
			return e
		}
		seen[g.ID] = true
		names[key] = true
	}
	return nil
}
func (s RecordingSettings) HasGroup(id string) bool {
	if id == "" {
		return true
	}
	for _, g := range s.Groups {
		if g.ID == id {
			return true
		}
	}
	return false
}
func (s RecordingSettings) GroupName(id string) string {
	for _, g := range s.Groups {
		if g.ID == id {
			return g.Name
		}
	}
	return ""
}
func (s RecordingSettings) AgeDays(c Camera) int {
	days := c.RetentionDays
	apply := func(n int) {
		if n > 0 && (days == 0 || n < days) {
			days = n
		}
	}
	apply(s.Global.MaxAgeDays)
	for _, g := range s.Groups {
		if g.ID == c.GroupID {
			apply(g.Limits.MaxAgeDays)
		}
	}
	return days
}
