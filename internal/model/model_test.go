package model

import "testing"

func TestCameraValidation(t *testing.T) {
	c := DefaultCamera()
	c.Name = "Pi"
	c.URL = "tcp://192.168.1.2:8554"
	if e := c.Validate(); e != nil {
		t.Fatal(e)
	}
	for _, mutate := range []func(*Camera){func(c *Camera) { c.ID = "../escape" }, func(c *Camera) { c.URL = "file:///etc/passwd" }, func(c *Camera) { c.URL = "tcp://camera:8554?listen=1" }, func(c *Camera) { c.FPS = 0 }, func(c *Camera) { c.Zones = []Zone{{Points: []Point{{X: -1, Y: 0}, {X: 1, Y: 0}, {X: 1, Y: 1}}}} }} {
		bad := c
		mutate(&bad)
		if bad.Validate() == nil {
			t.Fatalf("invalid camera accepted: %+v", bad)
		}
	}
}
