// Example: shared types, the in-memory notes map (created in Setup, see
// lifecycle.go) and an id helper.

package api

import "fmt"

type Note struct {
	ID   string `json:"id"`
	Text string `json:"text"`
	Tag  string `json:"tag,omitempty"`

	Priority int    `json:"priority,omitempty"`
	Comment  string `json:"comment,omitempty"`
	Notify   bool   `json:"notify,omitempty"`
}

type IDIn struct {
	ID string `json:"id" desc:"Note id"`
}

// The notes are kept in plain package variables to keep the examples short.
// They are not safe for concurrent calls: a real server guards its state with
// a mutex, as examples/todo does.
var notes map[string]Note

var seq int

func newID() string {
	seq++
	return fmt.Sprintf("note-%d", seq)
}
