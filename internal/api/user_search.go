package api

import (
	"errors"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
	"vartovyi/internal/model"
)

var errUserSearch = errors.New("invalid user search")

type userSearchToken struct {
	value  string
	quoted bool
}
type userSearchTerm struct {
	field, value string
	pattern      *regexp.Regexp
}

func parseUserSearch(query string) ([]userSearchTerm, error) {
	if !utf8.ValidString(query) || len(query) > 2048 || utf8.RuneCountInString(query) > 512 {
		return nil, errUserSearch
	}
	tokens := []userSearchToken{}
	for len(query) > 0 {
		query = strings.TrimLeftFunc(query, unicode.IsSpace)
		if query == "" {
			break
		}
		if query[0] == ':' {
			tokens = append(tokens, userSearchToken{value: ":"})
			query = query[1:]
			continue
		}
		if query[0] == '"' {
			end := strings.IndexByte(query[1:], '"')
			if end < 0 {
				return nil, errUserSearch
			}
			end++
			tokens = append(tokens, userSearchToken{value: query[1:end], quoted: true})
			query = query[end+1:]
			continue
		}
		end := 0
		for end < len(query) {
			r, size := utf8.DecodeRuneInString(query[end:])
			if unicode.IsSpace(r) || r == ':' || r == '"' {
				break
			}
			end += size
		}
		if end == 0 {
			return nil, errUserSearch
		}
		tokens = append(tokens, userSearchToken{value: query[:end]})
		query = query[end:]
	}
	terms := []userSearchTerm{}
	for i := 0; i < len(tokens); i++ {
		term := userSearchTerm{value: tokens[i].value}
		if !tokens[i].quoted && term.value == ":" {
			return nil, errUserSearch
		}
		if i+1 < len(tokens) && !tokens[i+1].quoted && tokens[i+1].value == ":" {
			if tokens[i].quoted {
				return nil, errUserSearch
			}
			term.field = strings.ToLower(term.value)
			if term.field != "user" && term.field != "team" {
				return nil, errUserSearch
			}
			i += 2
			if i >= len(tokens) || (!tokens[i].quoted && tokens[i].value == ":") {
				return nil, errUserSearch
			}
			term.value = tokens[i].value
		}
		if term.value == "" {
			return nil, errUserSearch
		}
		if strings.Contains(term.value, "*") {
			term.pattern = regexp.MustCompile("(?i)^" + strings.ReplaceAll(regexp.QuoteMeta(term.value), `\*`, ".*") + "$")
		}
		terms = append(terms, term)
		if len(terms) > 20 {
			return nil, errUserSearch
		}
	}
	return terms, nil
}
func roleSearchNames(role model.Role) []string {
	switch role {
	case model.Admin:
		return []string{"admin", "administrator", "administrators", "адмін", "адміністратор", "адміністратори"}
	case model.Manager:
		return []string{"manager", "managers", "менеджер", "менеджери"}
	case model.Viewer:
		return []string{"viewer", "viewers", "спостерігач", "спостерігачі"}
	}
	return []string{string(role)}
}
func (term userSearchTerm) matches(u model.User) bool {
	values := []string{u.Username, u.Name}
	values = append(values, roleSearchNames(u.Role)...)
	switch term.field {
	case "user":
		values = []string{u.Username}
	case "team":
		values = roleSearchNames(u.Role)
	}
	for _, value := range values {
		if term.pattern != nil {
			if term.pattern.MatchString(value) {
				return true
			}
		} else if term.field != "" {
			if strings.EqualFold(value, term.value) {
				return true
			}
		} else if strings.Contains(strings.ToLower(value), strings.ToLower(term.value)) {
			return true
		}
	}
	return false
}
func searchUsers(users []model.User, query string) ([]model.User, error) {
	terms, e := parseUserSearch(query)
	if e != nil {
		return nil, e
	}
	result := []model.User{}
	for _, user := range users {
		matched := true
		for _, term := range terms {
			if !term.matches(user) {
				matched = false
				break
			}
		}
		if matched {
			result = append(result, user)
		}
	}
	return result, nil
}
