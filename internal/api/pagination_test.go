package api

import (
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestPagination(t *testing.T) {
	for _, tt := range []struct {
		query                         string
		total, page, size, offset, to int
	}{{"", 0, 1, 10, 0, 0}, {"page=2&page_size=25", 80, 2, 25, 25, 50}, {"page=999&page_size=10", 32, 4, 10, 30, 32}, {"page=-1&page_size=13", 42, 1, 10, 0, 10}, {"page=999999999999999999999&page_size=100", 21, 1, 100, 0, 21}} {
		p := paginate(httptest.NewRequest("GET", "/users?"+tt.query, nil), tt.total)
		if p.Page != tt.page || p.Size != tt.size || p.Offset != tt.offset || p.To != tt.to {
			t.Fatalf("%s: %+v", tt.query, p)
		}
	}
	p := paginate(httptest.NewRequest("GET", "/users?q=user%3A+admin%2A&page=2&page_size=25", nil), 200)
	next, _ := url.Parse(p.Next)
	if next.Query().Get("q") != "user: admin*" || next.Query().Get("page") != "3" || next.Query().Get("page_size") != "25" {
		t.Fatal("search lost during navigation")
	}
	for _, filter := range p.Filters {
		if filter.Name == "page" || filter.Name == "page_size" {
			t.Fatal("page size control must reset the page")
		}
	}
	p = paginate(httptest.NewRequest("GET", "/events?page=500000", nil), 10000000)
	if len(p.Links) > 9 {
		t.Fatal("page links must stay bounded")
	}
}
