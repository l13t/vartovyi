package model

import "testing"

func TestRecordingPolicyValidationAndAge(t *testing.T) {
	s := RecordingSettings{Global: RecordingLimits{MaxAgeDays: 30}, Groups: []CameraGroup{{ID: "yard", Name: "Двір", Limits: RecordingLimits{MaxAgeDays: 10}}}}
	c := DefaultCamera()
	c.GroupID = "yard"
	if s.AgeDays(c) != 10 {
		t.Fatal("strictest age should apply")
	}
	c.RetentionDays = 5
	if s.AgeDays(c) != 5 {
		t.Fatal("camera age should apply")
	}
	c.RetentionDays = 0
	c.GroupID = ""
	if s.AgeDays(c) != 30 {
		t.Fatal("zero must not mean immediate expiry")
	}
	s.Global.MaxAgeDays = 0
	if s.AgeDays(c) != 0 {
		t.Fatal("all zero means unlimited age")
	}
	if e := s.Validate(); e != nil {
		t.Fatal(e)
	}
	s.Groups = append(s.Groups, CameraGroup{ID: "duplicate", Name: "двір"})
	if e := s.Validate(); e == nil {
		t.Fatal("duplicate group names accepted")
	}
	for _, limits := range []RecordingLimits{{MaxAgeDays: -1}, {MaxSegments: -1}, {MaxBytes: -1}, {MaxAgeDays: 3651}, {MaxBytes: 1 << 51}} {
		if limits.Validate() == nil {
			t.Fatal("invalid limits accepted")
		}
	}
}
