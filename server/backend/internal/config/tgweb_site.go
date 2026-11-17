package config

import (
	"embed"
	"io/fs"
	"sort"
)

// tgSite is the WEB proxy host's public site (TGWebSiteDir, the relay's public_dir): a neutral
// page of a fictional bookbinding studio, written once for this deployment and committed as an
// asset — never a shared starter (identical sites across operators are an active-probing
// signature, upstream PUBLIC_SITE.md). Exact routes only: /, /about.html, /styles.css,
// /favicon.svg; 404.html is the relay's not-found page. No external requests of any kind.
//
//go:embed tgsite/index.html tgsite/about.html tgsite/404.html tgsite/styles.css tgsite/favicon.svg
var tgSite embed.FS

// TGWebSiteFiles returns the site's files by name (index.html, …), sorted by name.
func TGWebSiteFiles() ([]string, map[string][]byte) {
	out := map[string][]byte{}
	var names []string
	entries, _ := fs.ReadDir(tgSite, "tgsite")
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		b, err := fs.ReadFile(tgSite, "tgsite/"+e.Name())
		if err != nil {
			continue
		}
		out[e.Name()] = b
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names, out
}
