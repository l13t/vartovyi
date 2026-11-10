package api

import (
	"testing"
	"vartovyi/internal/model"
)

func TestUserSearch(t *testing.T) {
	users := []model.User{{Username: "root", Name: "Олена", Role: model.Admin}, {Username: "admin-west", Name: "Оператор", Role: model.Viewer}, {Username: "ops", Name: "Admin helper", Role: model.Manager}}
	for _, tt := range []struct {
		query string
		want  int
	}{{"", 3}, {"admin", 3}, {"TEAM: admin", 1}, {"team:адміністратори", 1}, {"user: admin*", 1}, {"user: admin", 0}, {"user: *west", 1}, {"user: admin* team: viewer", 1}, {"team: admin user: admin*", 0}, {`"Admin helper"`, 1}, {"олена", 1}, {"user: [ops]", 0}, {"*", 3}} {
		got, e := searchUsers(users, tt.query)
		if e != nil || len(got) != tt.want {
			t.Fatalf("query %q: %d, %v", tt.query, len(got), e)
		}
	}
	for _, query := range []string{"team:", "team: user:", "unknown:admin", `user: "unterminated`, `: admin`, `user: ""`} {
		if _, e := searchUsers(users, query); e == nil {
			t.Fatalf("invalid query accepted: %q", query)
		}
	}
}
