package mcp

import (
	"mime"
	"net/http"
	"net/url"
	"path"
	"strings"
)

// resolveIcons makes relative icon sources absolute and infers missing MIME types.
// Relative sources are joined to the base URL option, or to the scheme and host of the request.
func (s *Server) resolveIcons(icons []Icon, r *http.Request) []Icon {
	if len(icons) == 0 {
		return nil
	}
	base := strings.TrimSuffix(s.opts.baseURL, "/")
	if base == "" && r != nil {
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		base = scheme + "://" + r.Host
	}
	out := make([]Icon, len(icons))
	for i, icon := range icons {
		if strings.HasPrefix(icon.Src, "data:") {
			out[i] = icon
			continue
		}
		u, err := url.Parse(icon.Src)
		if err != nil || (u.Scheme == "" && u.Host == "") {
			icon.Src = base + "/" + strings.TrimPrefix(icon.Src, "/")
			u = &url.URL{Path: u.Path}
		}
		if icon.MIMEType == "" {
			icon.MIMEType, _, _ = strings.Cut(mime.TypeByExtension(path.Ext(u.Path)), ";")
		}
		out[i] = icon
	}
	return out
}
