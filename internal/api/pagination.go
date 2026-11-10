package api

import (
	"net/http"
	"net/url"
	"sort"
	"strconv"
)

type pageLink struct {
	Number       int
	URL          string
	Current, Gap bool
}
type pageFilter struct{ Name, Value string }
type pagination struct {
	Page, Size, Pages, Total, From, To, Offset int
	Prev, Next, Path                           string
	Links                                      []pageLink
	Filters                                    []pageFilter
}

func pageRequest(r *http.Request) (int, int) {
	page, e := strconv.Atoi(r.URL.Query().Get("page"))
	if e != nil || page < 1 {
		page = 1
	}
	size, e := strconv.Atoi(r.URL.Query().Get("page_size"))
	if e != nil || (size != 10 && size != 25 && size != 50 && size != 100) {
		size = 10
	}
	return page, size
}
func paginate(r *http.Request, total int) pagination {
	page, size := pageRequest(r)
	pages := total / size
	if total%size > 0 {
		pages++
	}
	if pages < 1 {
		pages = 1
	}
	if page > pages {
		page = pages
	}
	p := pagination{Page: page, Size: size, Pages: pages, Total: total, Path: r.URL.Path, Offset: (page - 1) * size, Links: []pageLink{}, Filters: []pageFilter{}}
	if total > 0 {
		p.From = p.Offset + 1
		p.To = p.Offset + size
		if p.To > total {
			p.To = total
		}
	}
	query := r.URL.Query()
	query.Del("page")
	query.Del("page_size")
	// Encode/parse gives a deterministic key order for hidden filter fields.
	ordered, _ := url.ParseQuery(query.Encode())
	keys := []string{}
	for key := range ordered {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		for _, value := range ordered[key] {
			p.Filters = append(p.Filters, pageFilter{key, value})
		}
	}
	link := func(number int) string {
		q := r.URL.Query()
		q.Set("page_size", strconv.Itoa(size))
		if number == 1 {
			q.Del("page")
		} else {
			q.Set("page", strconv.Itoa(number))
		}
		return r.URL.Path + "?" + q.Encode()
	}
	if page > 1 {
		p.Prev = link(page - 1)
	}
	if page < pages {
		p.Next = link(page + 1)
	}
	previous := 0
	for n := 1; n <= pages; {
		if n == 1 || n == pages || (n >= page-2 && n <= page+2) {
			if previous > 0 && n > previous+1 {
				p.Links = append(p.Links, pageLink{Gap: true})
			}
			p.Links = append(p.Links, pageLink{Number: n, URL: link(n), Current: n == page})
			previous = n
		}
		if n == 1 && page-2 > 2 {
			n = page - 2
		} else if n >= page+2 && n < pages {
			n = pages
		} else {
			n++
		}
	}
	return p
}
