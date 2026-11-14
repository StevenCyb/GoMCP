// Example: shared types, the in-memory notes map (created in Setup, see
// lifecycle.go) and an id helper.

package api

import "fmt"

type Note struct {
	ID   string `json:"id"`
	Text string `json:"text"`
	Tag  string `json:"tag,omitempty"`
}

type IDIn struct {
	ID string `json:"id" desc:"Note id"`
}

var notes map[string]Note

var seq int

func newID() string {
	seq++
	return fmt.Sprintf("note-%d", seq)
}
