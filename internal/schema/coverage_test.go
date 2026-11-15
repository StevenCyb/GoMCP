package schema_test

import (
	"testing"

	"github.com/StevenCyb/GoMCP/internal/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBindURITypes(t *testing.T) {
	type vars struct {
		S string `uri:"s"`
		I int64  `uri:"i"`
		U uint16 `uri:"u"`
		B bool   `uri:"b"`
	}
	var v vars
	require.NoError(t, schema.BindURI(map[string]string{"s": "a b", "i": "-9", "u": "65535", "b": "true"}, &v))
	assert.Equal(t, vars{S: "a b", I: -9, U: 65535, B: true}, v)

	for name, in := range map[string]map[string]string{
		"bad int":  {"i": "1.5"},
		"bad uint": {"u": "-1"},
		"bad bool": {"b": "maybe"},
	} {
		err := schema.BindURI(in, &vars{})
		require.Error(t, err, name)
		assert.Contains(t, err.Error(), "uri variable", name)
	}

	type floaty struct {
		F float64 `uri:"f"`
	}
	err := schema.BindURI(map[string]string{"f": "1.5"}, &floaty{})
	require.ErrorContains(t, err, "unsupported type float64")
}
