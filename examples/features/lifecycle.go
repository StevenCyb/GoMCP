// Example: package-level startup and teardown hooks, run once when the server
// starts and stops (here: create and drop the in-memory notes map).

package api

import (
	"log"
	"time"
)

var startedAt time.Time

// Example: runs once on startup (create the notes map).
//
// @MCP.Setup
func Setup() error {
	log.Println("starting notes server")
	notes = map[string]Note{}
	startedAt = time.Now()
	return nil
}

// Example: runs once on shutdown (drop the notes map).
//
// @MCP.Teardown
func Teardown() {
	log.Printf("stopping notes server after %s", time.Since(startedAt))
	notes = nil
}
