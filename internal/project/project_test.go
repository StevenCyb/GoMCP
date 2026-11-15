package project_test

import (
	"testing"

	"github.com/StevenCyb/GoMCP/internal/project"
	"github.com/stretchr/testify/assert"
)

func TestPosString(t *testing.T) {
	tests := []struct {
		name string
		pos  project.Pos
		want string
	}{
		{"with column", project.Pos{File: "api/tools.go", Line: 12, Col: 3}, "api/tools.go:12:3"},
		{"without column", project.Pos{File: "api/tools.go", Line: 12}, "api/tools.go:12"},
		{"zero value", project.Pos{}, ":0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.pos.String())
		})
	}
}

func TestPosFormatsInMessages(t *testing.T) {
	assert.Equal(t, "error at a.go:1:2", "error at "+project.Pos{File: "a.go", Line: 1, Col: 2}.String())
}
