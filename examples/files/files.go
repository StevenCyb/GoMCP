// Example: a read-only browser for a directory, meant to run over stdio as a local tool.
// The root comes from FILES_ROOT (default: the working directory). Every path a client
// sends goes through resolve, so nothing outside the root can be read.

// @MCP.Server("files", version="1.0.0")
// @MCP.Instructions("Browse and read text files below one root directory. Paths are relative to it.")
package api

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/StevenCyb/GoMCP/pkg/mcp"
)

const maxFileSize = 1 << 20 // larger files are refused

var root string

// @MCP.Setup
func Setup() error {
	dir := os.Getenv("FILES_ROOT")
	if dir == "" {
		dir = "."
	}
	abs, err := filepath.Abs(dir)
	root = abs
	return err
}

// resolve maps a client path to a path below root and fails when it would leave root.
// ponytail: symlinks inside root are followed; resolve them with EvalSymlinks if root holds untrusted links.
func resolve(root, rel string) (string, error) {
	p := filepath.Join(root, filepath.FromSlash(rel)) // Join cleans the path, so ".." is already folded
	back, err := filepath.Rel(root, p)
	if err != nil || back == ".." || strings.HasPrefix(back, ".."+string(filepath.Separator)) {
		return "", errors.New("path is outside the root")
	}
	return p, nil
}

// search returns the slash separated paths of all files below root whose name contains substr.
func search(root, substr string) ([]string, error) {
	out := []string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.Contains(strings.ToLower(d.Name()), strings.ToLower(substr)) {
			return err
		}
		rel, err := filepath.Rel(root, p)
		out = append(out, filepath.ToSlash(rel))
		return err
	})
	return out, err
}

type FileURI struct {
	Path string `uri:"path"`
}

// Example: resource template for text files directly in the root, e.g.
// file:///readme.txt. A variable matches one path segment, so files in
// subdirectories are not reachable through it.
//
// @MCP.Resource("file:///{path}", title="File", mime="text/plain", description="Reads a text file in the root")
func Read(ctx mcp.Context, u FileURI) (mcp.Contents, error) {
	p, err := resolve(root, u.Path)
	if err != nil {
		return nil, mcp.ErrNotFound
	}
	if info, err := os.Stat(p); err != nil || info.IsDir() || info.Size() > maxFileSize {
		return nil, mcp.ErrNotFound
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, mcp.ErrNotFound
	}
	return mcp.Contents{mcp.TextContent(string(data))}, nil
}

type ListIn struct {
	Dir string `json:"dir,omitempty" desc:"Directory relative to the root, default: the root itself"`
}

type Names struct {
	Files []string `json:"files"`
}

// @MCP.Tool("list_files", description="Lists the entries of a directory below the root; directories end with /")
// @MCP.Hint(readOnly, idempotent)
func ListFiles(ctx mcp.Context, in ListIn) (Names, error) {
	p, err := resolve(root, in.Dir)
	if err != nil {
		return Names{}, mcp.ToolError(err.Error())
	}
	entries, err := os.ReadDir(p)
	if err != nil {
		return Names{}, mcp.ToolError("cannot list " + in.Dir)
	}
	out := Names{Files: []string{}}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			name += "/"
		}
		out.Files = append(out.Files, name)
	}
	return out, nil
}

type SearchIn struct {
	Query string `json:"query" desc:"Part of a file name, case-insensitive" min:"1"`
}

// @MCP.Tool("search_files", description="Finds files below the root whose name contains the query")
// @MCP.Hint(readOnly, idempotent)
func SearchFiles(ctx mcp.Context, in SearchIn) (Names, error) {
	found, err := search(root, in.Query)
	if err != nil {
		return Names{}, mcp.ToolError("search failed")
	}
	return Names{Files: found}, nil
}
