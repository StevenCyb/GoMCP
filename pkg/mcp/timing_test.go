package mcp_test

import "time"

func timeAfter() <-chan time.Time { return time.After(3 * time.Second) }
func shortWait() <-chan time.Time { return time.After(150 * time.Millisecond) }
