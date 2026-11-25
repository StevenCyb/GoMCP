// Example: resource template with subscriptions and annotations, paginated
// resource listing, and a static resource.

package api

import "github.com/StevenCyb/GoMCP/pkg/mcp"

type NoteURI struct {
	ID string `uri:"id"`
}

// Example: resource template with subscriptions and annotations.
//
// @MCP.Resource("note:///{id}", title="Note", mime="text/plain", description="Reads a single note by id")
// @MCP.Subscribable
// @MCP.AuthScopes("notes:read")
// @MCP.Annotations(audience="user,assistant", priority=0.8)
func Read(ctx mcp.Context, u NoteURI) (mcp.Contents, error) {
	note, ok := notes[u.ID]
	if !ok {
		return nil, mcp.ErrNotFound
	}
	return mcp.Contents{mcp.TextContent(note.Text)}, nil
}

// Example: listing of the concrete URIs of the template above. Return all of
// them; the server pages the merged list (see @MCP.PageSize).
//
// @MCP.ResourceList("note:///{id}")
func List(ctx mcp.Context, _ mcp.PageReq) (mcp.Page[mcp.ResourceRef], error) {
	var refs []mcp.ResourceRef
	for id := range notes {
		refs = append(refs, mcp.ResourceRef{URI: "note:///" + id, Name: id})
	}
	return mcp.Page[mcp.ResourceRef]{Items: refs}, nil
}

// Example: static resource with a fixed URI; it wins over the template above
// for `note:///index`.
//
// @MCP.Resource("note:///index", mime="application/json", description="Ids of all notes")
func Index(ctx mcp.Context) (mcp.Contents, error) {
	ids := make([]string, 0, len(notes))
	for id := range notes {
		ids = append(ids, id)
	}
	return mcp.JSONContents(ids)
}
