package web

import (
	"embed"
	"html/template"
	"io/fs"
	"net/http"
	"vartovyi/internal/buildinfo"
)

//go:embed templates/*.html static/* static/vendor/*
var files embed.FS

type Web struct{ templates *template.Template }

func New() (*Web, error) {
	t, e := template.New("pages").Funcs(template.FuncMap{"tr": Translate, "role": roleLabel,
		"version": func() string { return buildinfo.Version }, "license": func() string { return buildinfo.License },
	}).ParseFS(files, "templates/*.html")
	return &Web{t}, e
}
func (w *Web) Render(out http.ResponseWriter, name string, data any) {
	out.Header().Set("Content-Type", "text/html; charset=utf-8")
	out.Header().Set("Cache-Control", "no-store")
	if e := w.templates.ExecuteTemplate(out, name, data); e != nil {
		http.Error(out, "render failed", 500)
	}
}
func (w *Web) Static() http.Handler {
	f, _ := fs.Sub(files, "static")
	return http.FileServer(http.FS(f))
}
