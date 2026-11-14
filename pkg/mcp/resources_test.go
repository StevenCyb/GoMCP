package mcp_test

import (
	"encoding/base64"
	"errors"
	"fmt"
	"testing"

	"github.com/StevenCyb/GoMCP/internal/rpc"
	"github.com/StevenCyb/GoMCP/pkg/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	noteTemplate = "note:///{id}"
	indexURI     = "note:///index"
)

type noteURI struct {
	ID string `uri:"id"`
}

type resourceEntry struct {
	URI         string         `json:"uri"`
	URITemplate string         `json:"uriTemplate"`
	Name        string         `json:"name"`
	Title       string         `json:"title"`
	Description string         `json:"description"`
	MIMEType    string         `json:"mimeType"`
	Annotations map[string]any `json:"annotations"`
	Icons       []mcp.Icon     `json:"icons"`
	Meta        map[string]any `json:"_meta"` //nolint:tagliatelle // field name defined by the MCP specification
}

type resourceList struct {
	Resources         []resourceEntry `json:"resources"`
	ResourceTemplates []resourceEntry `json:"resourceTemplates"`
	NextCursor        string          `json:"nextCursor"`
}

type readResult struct {
	Contents []struct {
		URI      string `json:"uri"`
		MIMEType string `json:"mimeType"`
		Text     string `json:"text"`
		Blob     string `json:"blob"`
	} `json:"contents"`
}

func resourcesServer(t *testing.T, opts ...mcp.Option) (*mcp.Server, *testClient) {
	t.Helper()
	s := newBasicServer(opts...)
	require.NoError(t, mcp.AddStaticResource(s, mcp.ResourceDef{URI: indexURI, Name: "index", MIMEType: "application/json", Description: "Ids"},
		func(mcp.Context) (mcp.Contents, error) { return mcp.JSONContents([]string{idNote1, "note-2"}) }))
	require.NoError(t, mcp.AddResource(s, mcp.ResourceDef{
		URI: noteTemplate, Name: "note", Title: "Note", Description: "A note", MIMEType: "text/plain",
		Subscribable: true,
		Annotations:  &mcp.Annotations{Audience: []mcp.Role{mcp.RoleUser, roleAssistant}, Priority: new(0.8)},
		Icons:        []mcp.Icon{{Src: "https://example.com/n.svg"}},
		Meta:         map[string]any{keyTeam: valDocs},
	}, func(_ mcp.Context, u noteURI) (mcp.Contents, error) {
		switch u.ID {
		case "missing":
			return nil, mcp.ErrNotFound
		case "boom":
			return nil, errors.New("disk on fire")
		case "blob":
			return mcp.Contents{{Blob: base64.StdEncoding.EncodeToString([]byte("png")), MIMEType: mimePNG}}, nil
		}
		return mcp.Contents{mcp.TextContent("text of " + u.ID)}, nil
	}))
	require.NoError(t, s.AddResourceLister(noteTemplate, func(_ mcp.Context, page mcp.PageReq) (mcp.Page[mcp.ResourceRef], error) {
		refs := []mcp.ResourceRef{{URI: "note:///note-1", Name: idNote1}, {URI: "note:///note-2", Name: "note-2"}}
		return mcp.Paginate(refs, page), nil
	}))
	return s, connect(t, newServer(t, s), nil)
}

func readURI(t *testing.T, c *testClient, uri string) rpc.Message {
	t.Helper()
	return c.call("resources/read", map[string]any{keyURI: uri})
}

func TestResourcesList(t *testing.T) {
	_, c := resourcesServer(t)
	list := result[resourceList](t, c.call("resources/list", nil))
	require.Len(t, list.Resources, 3, "static resource plus the listed ones")
	assert.Equal(t, indexURI, list.Resources[0].URI)
	assert.Equal(t, "Ids", list.Resources[0].Description)
	assert.Equal(t, "application/json", list.Resources[0].MIMEType)
	assert.Equal(t, "note:///note-1", list.Resources[1].URI)
	assert.Equal(t, "note-2", list.Resources[2].Name)
}

func TestResourcesListPagination(t *testing.T) {
	_, c := resourcesServer(t, mcp.WithPageSize(2))
	first := result[resourceList](t, c.call("resources/list", nil))
	require.Len(t, first.Resources, 2)
	require.NotEmpty(t, first.NextCursor)

	second := result[resourceList](t, c.call("resources/list", map[string]any{"cursor": first.NextCursor}))
	require.Len(t, second.Resources, 1)
	assert.Empty(t, second.NextCursor)
}

func TestResourceTemplatesList(t *testing.T) {
	_, c := resourcesServer(t)
	list := result[resourceList](t, c.call("resources/templates/list", nil))
	require.Len(t, list.ResourceTemplates, 1)
	tmpl := list.ResourceTemplates[0]
	assert.Equal(t, noteTemplate, tmpl.URITemplate)
	assert.Equal(t, "note", tmpl.Name)
	assert.Equal(t, "Note", tmpl.Title)
	assert.Equal(t, "text/plain", tmpl.MIMEType)
	assert.Equal(t, map[string]any{"audience": []any{"user", roleAssistant}, "priority": 0.8}, tmpl.Annotations)
	assert.Len(t, tmpl.Icons, 1)
	assert.Equal(t, map[string]any{keyTeam: valDocs}, tmpl.Meta)
}

func TestReadStaticResource(t *testing.T) {
	_, c := resourcesServer(t)
	res := result[readResult](t, readURI(t, c, indexURI))
	require.Len(t, res.Contents, 1)
	assert.Equal(t, indexURI, res.Contents[0].URI)
	assert.Equal(t, "application/json", res.Contents[0].MIMEType)
	assert.JSONEq(t, `["note-1","note-2"]`, res.Contents[0].Text)
}

func TestReadTemplateResource(t *testing.T) {
	_, c := resourcesServer(t)

	res := result[readResult](t, readURI(t, c, "note:///note-7"))
	require.Len(t, res.Contents, 1)
	assert.Equal(t, "note:///note-7", res.Contents[0].URI)
	assert.Equal(t, "text/plain", res.Contents[0].MIMEType, "defaults to the MIME type of the definition")
	assert.Equal(t, "text of note-7", res.Contents[0].Text)

	blob := result[readResult](t, readURI(t, c, "note:///blob"))
	assert.Equal(t, mimePNG, blob.Contents[0].MIMEType, "content keeps its own MIME type")
	assert.NotEmpty(t, blob.Contents[0].Blob)
}

func TestReadExactURIWinsOverTemplate(t *testing.T) {
	_, c := resourcesServer(t)
	res := result[readResult](t, readURI(t, c, indexURI))
	assert.JSONEq(t, `["note-1","note-2"]`, res.Contents[0].Text, "note:///index is the static resource, not template id=index")
}

func TestReadErrors(t *testing.T) {
	_, c := resourcesServer(t)

	unknown := readURI(t, c, "other:///x")
	require.NotNil(t, unknown.Error)
	assert.Equal(t, rpc.CodeInvalidParams, unknown.Error.Code)

	missing := readURI(t, c, "note:///missing")
	require.NotNil(t, missing.Error)
	assert.Equal(t, rpc.CodeInvalidParams, missing.Error.Code)
	assert.Contains(t, missing.Error.Message, "note:///missing", "the error names the resource, also when the handler returned ErrNotFound")
	assert.Contains(t, unknown.Error.Message, "other:///x")

	boom := readURI(t, c, "note:///boom")
	require.NotNil(t, boom.Error)
	assert.Equal(t, rpc.CodeInternal, boom.Error.Code)

	bad := c.call("resources/read", map[string]any{})
	require.NotNil(t, bad.Error)
	assert.Equal(t, rpc.CodeInvalidParams, bad.Error.Code)
}

func TestResourceMiddleware(t *testing.T) {
	s, _ := resourcesServer(t)
	var seen []string
	s.Use(func(ctx mcp.Context, req mcp.Request, next mcp.Handler) (any, error) {
		seen = append(seen, fmt.Sprintf("%s:%s", req.Kind, req.Name))
		return next(ctx, req)
	})
	c := connect(t, newServer(t, s), nil)
	_ = readURI(t, c, "note:///n1")
	assert.Equal(t, []string{"resource:note:///n1"}, seen)
}

func TestListerError(t *testing.T) {
	s := newBasicServer()
	require.NoError(t, mcp.AddResource(s, mcp.ResourceDef{URI: noteTemplate, Name: "n"},
		func(mcp.Context, noteURI) (mcp.Contents, error) { return nil, nil }))
	require.NoError(t, s.AddResourceLister(noteTemplate, func(mcp.Context, mcp.PageReq) (mcp.Page[mcp.ResourceRef], error) {
		return mcp.Page[mcp.ResourceRef]{}, errors.New("db down")
	}))
	c := connect(t, newServer(t, s), nil)
	res := c.call("resources/list", nil)
	require.NotNil(t, res.Error)
	assert.Equal(t, rpc.CodeInternal, res.Error.Code)
}

func TestResourceRegistrationErrors(t *testing.T) {
	s := newBasicServer()
	handler := func(mcp.Context, noteURI) (mcp.Contents, error) { return nil, nil }
	static := func(mcp.Context) (mcp.Contents, error) { return nil, nil }

	require.Error(t, mcp.AddResource(s, mcp.ResourceDef{Name: "x"}, handler), "URI required")
	require.Error(t, mcp.AddResource(s, mcp.ResourceDef{URI: "note:///fixed"}, handler), "AddResource needs a template")
	require.Error(t, mcp.AddStaticResource(s, mcp.ResourceDef{}, static), "URI required")
	require.Error(t, mcp.AddStaticResource(s, mcp.ResourceDef{URI: noteTemplate}, static), "AddStaticResource needs a fixed URI")
	require.Error(t, mcp.AddResource(s, mcp.ResourceDef{URI: "note:///{other}"}, handler), "uri tag must match a template variable")

	require.NoError(t, mcp.AddResource(s, mcp.ResourceDef{URI: noteTemplate}, handler))
	require.Error(t, mcp.AddResource(s, mcp.ResourceDef{URI: noteTemplate}, handler), "duplicate template")
	require.NoError(t, mcp.AddStaticResource(s, mcp.ResourceDef{URI: indexURI}, static))
	require.Error(t, mcp.AddStaticResource(s, mcp.ResourceDef{URI: indexURI}, static), "duplicate static resource")

	require.Error(t, s.AddResourceLister("other:///{x}", func(mcp.Context, mcp.PageReq) (mcp.Page[mcp.ResourceRef], error) {
		return mcp.Page[mcp.ResourceRef]{}, nil
	}), "lister needs a registered template")
}
