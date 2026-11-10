package web

import (
	"encoding/json"
	"fmt"
)

var english = func() map[string]string {
	b, e := files.ReadFile("static/en.json")
	if e != nil {
		panic(e)
	}
	var v map[string]string
	if e = json.Unmarshal(b, &v); e != nil {
		panic(e)
	}
	return v
}()

func Translate(language any, text string) string {
	if language == "en" {
		if v, ok := english[text]; ok {
			return v
		}
	}
	if value, ok := ukrainian[text]; ok {
		return value
	}
	return text
}
func roleLabel(role any) string {
	// Role is a defined string type.
	switch stringRole := fmt.Sprint(role); stringRole {
	case "admin":
		return "Адміністратор"
	case "manager":
		return "Менеджер"
	}
	return "Спостерігач"
}

var ukrainian = func() map[string]string {
	b, e := files.ReadFile("static/uk.json")
	if e != nil {
		panic(e)
	}
	var v map[string]string
	if e = json.Unmarshal(b, &v); e != nil {
		panic(e)
	}
	return v
}()
