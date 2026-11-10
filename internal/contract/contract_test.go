package contract

import (
	"encoding/json"
	"testing"
)

func TestPublicContract(t *testing.T) {
	var doc struct {
		OpenAPI string                    `json:"openapi"`
		Paths   map[string]map[string]any `json:"paths"`
	}
	if e := json.Unmarshal(OpenAPI, &doc); e != nil {
		t.Fatal(e)
	}
	if doc.OpenAPI != "3.1.0" {
		t.Fatal("missing version")
	}
	for _, path := range []string{"/api/v1/cameras", "/api/v1/events", "/api/v1/exports", "/media/{camera_id}/live/{resource}", "/api/v1/stream"} {
		if _, ok := doc.Paths[path]; !ok {
			t.Fatal("missing route", path)
		}
	}
}
