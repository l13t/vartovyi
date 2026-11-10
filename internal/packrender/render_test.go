package packrender

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"text/template"
)

func TestRuntimeConfigurationTemplates(t *testing.T) {
	v := values(t)
	b, e := Render("../../packs/vartovyi", v)
	if e != nil {
		t.Fatal(e)
	}
	blocks := regexp.MustCompile(`(?s)data = <<EOH\n(.*?)\nEOH`).FindAllSubmatch(b, -1)
	type item struct{ Value string }
	funcs := template.FuncMap{"nomadVar": func(string) map[string]item {
		return map[string]item{"service_token": {"private-service-token"}, "encryption_key": {"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="}, "postgres_password": {"password with @?#"}, "nats_password": {"broker password"}, "admin_password": {"administrator-password"}, "ingress_nomad_token": {"ingress-token"}}
	}, "nomadService": func(string) []map[string]any { return []map[string]any{{"Address": "10.0.0.1", "Port": 12345}} }, "env": func(string) string { return "12345" }, "toJSON": func(v any) string { b, _ := json.Marshal(v); return string(b) }}
	jsonCount := 0
	for _, block := range blocks {
		t1, e := template.New("runtime").Funcs(funcs).Parse(string(block[1]))
		if e != nil {
			t.Fatal(e)
		}
		var rendered bytes.Buffer
		if e = t1.Execute(&rendered, nil); e != nil {
			t.Fatal(e)
		}
		out := bytes.TrimSpace(rendered.Bytes())
		if len(out) > 0 && out[0] == '{' {
			jsonCount++
			if !json.Valid(out) {
				t.Fatalf("invalid runtime JSON: %s", out)
			}
		}
	}
	if jsonCount < 5 {
		t.Fatalf("expected runtime JSON for all application tasks, got %d", jsonCount)
	}
}

func values(t *testing.T) map[string]any {
	t.Helper()
	b, e := os.ReadFile("../../packs/vartovyi/testdata/defaults.json")
	if e != nil {
		t.Fatal(e)
	}
	var v map[string]any
	if e = json.Unmarshal(b, &v); e != nil {
		t.Fatal(e)
	}
	return v
}
func TestRenderAndNomadValidate(t *testing.T) {
	for _, profile := range []string{"bundled", "external", "csi", "host-network"} {
		t.Run(profile, func(t *testing.T) {
			v := values(t)
			switch profile {
			case "external":
				v["ingress_mode"] = "external"
				v["postgres_enabled"] = false
				v["nats_enabled"] = false
			case "csi":
				v["ingress_mode"] = "disabled"
				shards := v["shards"].([]any)
				shards[0].(map[string]any)["volume_type"] = "csi"
				shards = append(shards, map[string]any{"id": "second", "volume_type": "host", "volume_source": "second", "quota_bytes": float64(1000000000)})
				v["shards"] = shards
			case "host-network":
				v["api_network_mode"] = "host"
			}
			b, e := Render("../../packs/vartovyi", v)
			if e != nil {
				t.Fatal(e)
			}
			if !strings.Contains(string(b), "nomadVar") || !strings.Contains(string(b), ".service_token.Value | toJSON") {
				t.Fatal("runtime secret references missing")
			}
			path := filepath.Join(t.TempDir(), "job.nomad.hcl")
			if e = os.WriteFile(path, b, 0600); e != nil {
				t.Fatal(e)
			}
			if _, e = exec.LookPath("nomad"); e != nil {
				t.Skip("nomad CLI unavailable")
			}
			cmd := exec.Command("nomad", "job", "validate", path)
			out, e := cmd.CombinedOutput()
			if e != nil {
				t.Fatalf("nomad validation failed: %v\n%s\n%s", e, out, b)
			}
		})
	}
}
func TestInvalidShardRejected(t *testing.T) {
	for _, id := range []string{"../escape", "same"} {
		v := values(t)
		shard := v["shards"].([]any)[0].(map[string]any)
		shard["id"] = id
		if id == "same" {
			v["shards"] = []any{shard, shard}
		}
		if _, e := Render("../../packs/vartovyi", v); e == nil {
			t.Fatalf("accepted %s", id)
		}
	}
}

func TestPackVariableTypesParseAsHCL(t *testing.T) {
	if _, e := exec.LookPath("nomad"); e != nil {
		t.Skip("nomad CLI unavailable")
	}
	b, e := os.ReadFile("../../packs/vartovyi/variables.hcl")
	if e != nil {
		t.Fatal(e)
	}
	b = append(b, []byte(`
job "pack-variable-syntax" {
  group "check" {
    task "check" {
      driver = "raw_exec"
      config { command = "true" }
    }
  }
}
`)...)
	path := filepath.Join(t.TempDir(), "variables.nomad.hcl")
	if e = os.WriteFile(path, b, 0600); e != nil {
		t.Fatal(e)
	}
	if out, e := exec.Command("nomad", "job", "validate", path).CombinedOutput(); e != nil {
		t.Fatalf("invalid variable HCL: %v\n%s", e, out)
	}
}
