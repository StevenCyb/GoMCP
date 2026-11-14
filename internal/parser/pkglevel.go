package parser

import (
	"strings"
	"time"

	"github.com/StevenCyb/GoMCP/internal/project"
)

// packageLevel handles the annotations of floating and package doc comments.
func (c *collector) packageLevel(f *file, a annotated) {
	for _, ann := range a.anns {
		c.packageAnnotation(f, ann)
	}
}

func (c *collector) packageAnnotation(f *file, ann located) {
	switch ann.Name {
	case "Server":
		c.server(ann)
	case "Icon":
		if icon, ok := c.icon(ann); ok {
			c.p.Server.Icons = append(c.p.Server.Icons, icon)
		}
	case "Instructions":
		c.instructions(ann)
	case "Endpoint":
		c.endpoint(ann)
	case "AllowedOrigin":
		c.allowedOrigin(ann)
	case "KeepAlive":
		if d, ok := c.duration(ann, ann.Positionals()); ok {
			c.p.Transport.KeepAlive = d
		}
	case "PageSize":
		c.pageSize(ann)
	case "Cache":
		c.cache(ann)
	case "Use":
		if ref, ok := c.useRef(f, ann); ok {
			c.p.Middleware = append(c.p.Middleware, ref)
		}
	default:
		if isFeatureOrModifier(ann.Name) {
			c.errorf(ann.pos, "annotation %s must be placed on a function or declaration, not in a package comment", ann.Name)
		} else {
			c.errorf(ann.pos, "unknown annotation %q", ann.Name)
		}
	}
}

func (c *collector) instructions(ann located) {
	if text, ok := ann.Positional(); ok {
		c.p.Server.Instructions = text
		return
	}
	c.errorf(ann.pos, "Instructions needs a text")
}

func (c *collector) allowedOrigin(ann located) {
	if origin, ok := ann.Positional(); ok {
		c.p.Transport.AllowedOrigins = append(c.p.Transport.AllowedOrigins, origin)
		return
	}
	c.errorf(ann.pos, "AllowedOrigin needs an origin")
}

func (c *collector) pageSize(ann located) {
	if n, ok := ann.PositionalNumber(); ok && n >= 0 {
		c.p.Transport.PageSize = int(n)
		return
	}
	c.errorf(ann.pos, "PageSize needs a number")
}

func (c *collector) cache(ann located) {
	ttl, ok := c.duration(ann, ann.Positionals())
	if !ok {
		return
	}
	scope, _ := ann.String("scope")
	switch scope {
	case "":
		scope = "public"
	case "public", "private":
	default:
		c.errorf(ann.pos, "unknown cache scope %q, use public or private", scope)
		return
	}
	c.p.Transport.CacheTTL, c.p.Transport.CacheScope = ttl, scope
}

func (c *collector) server(ann located) {
	if c.serverSeen {
		c.errorf(ann.pos, "more than one Server annotation")
		return
	}
	c.serverSeen = true
	s := &c.p.Server
	if name, ok := ann.Positional(); ok {
		s.Name = name
	} else {
		c.errorf(ann.pos, "Server needs a name")
	}
	s.Title, _ = ann.String("title")
	s.Version, _ = ann.String("version")
	s.Description, _ = ann.String("description")
	s.Website, _ = ann.String("website")
	s.BaseURL, _ = ann.String("baseURL")
}

func (c *collector) endpoint(ann located) {
	ep, ok := ann.Positional()
	if !ok || !strings.HasPrefix(ep, "/") {
		c.errorf(ann.pos, "Endpoint needs a path starting with /")
		return
	}
	c.p.Transport.Endpoint = ep
}

// duration validates a duration given as the first value.
func (c *collector) duration(ann located, values []string) (string, bool) {
	if len(values) == 0 {
		c.errorf(ann.pos, "%s needs a duration such as \"30s\"", ann.Name)
		return "", false
	}
	if _, err := time.ParseDuration(values[0]); err != nil {
		c.errorf(ann.pos, "invalid duration %q", values[0])
		return "", false
	}
	return values[0], true
}

// icon reads an Icon annotation.
func (c *collector) icon(ann located) (project.Icon, bool) {
	src, ok := ann.Positional()
	if !ok {
		c.errorf(ann.pos, "Icon needs a source")
		return project.Icon{}, false
	}
	icon := project.Icon{Src: src}
	icon.MIME, _ = ann.String("mime")
	icon.Theme, _ = ann.String("theme")
	if sizes, ok := ann.String("sizes"); ok {
		icon.Sizes = strings.Split(sizes, ",")
	}
	return icon, true
}

func isFeatureOrModifier(name string) bool {
	_, feature := featureModifiers[name]
	if feature {
		return true
	}
	for _, mods := range featureModifiers {
		if mods[name] {
			return true
		}
	}
	return name == "Elicitation" || name == "ServeAssets"
}
