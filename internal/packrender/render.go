// Package packrender exercises pack Go templates offline. Production deployment
// must use nomad-pack; this harness deliberately does not parse HCL variables.
package packrender

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"
)

func Render(pack string, values map[string]any) ([]byte, error) {
	funcs := template.FuncMap{
		"int": func(v any) int {
			switch n := v.(type) {
			case int:
				return n
			case float64:
				return int(n)
			default:
				return 0
			}
		},
		"var": func(key string, root map[string]any) (any, error) {
			v, ok := root[key]
			if !ok {
				return nil, fmt.Errorf("missing variable %s", key)
			}
			return v, nil
		},
		"toJson": func(v any) (string, error) { b, e := json.Marshal(v); return string(b), e },
		"dict": func(v ...any) (map[string]any, error) {
			if len(v)%2 != 0 {
				return nil, errors.New("invalid dict")
			}
			m := map[string]any{}
			for i := 0; i < len(v); i += 2 {
				m[fmt.Sprint(v[i])] = v[i+1]
			}
			return m, nil
		},
		"list":       func(v ...any) []any { return v },
		"fail":       func(message string) (string, error) { return "", errors.New(message) },
		"regexMatch": func(pattern, value string) bool { b, _ := regexp.MatchString(pattern, value); return b },
		"hasKey":     func(m map[string]any, k string) bool { _, ok := m[k]; return ok },
		"set":        func(m map[string]any, k string, v any) map[string]any { m[k] = v; return m },
		"trimPrefix": func(prefix, value string) string { return strings.TrimPrefix(value, prefix) },
	}
	t, e := template.New("pack").Delims("[[", "]]").Funcs(funcs).ParseGlob(filepath.Join(pack, "templates", "*.tpl"))
	if e != nil {
		return nil, e
	}
	var b bytes.Buffer
	e = t.ExecuteTemplate(&b, "vartovyi.nomad.tpl", values)
	return b.Bytes(), e
}
