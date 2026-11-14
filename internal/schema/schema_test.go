package schema_test

import (
	"encoding/json"
	"maps"
	"reflect"
	"testing"
	"time"

	"github.com/StevenCyb/GoMCP/internal/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type addIn struct {
	Text string `desc:"Note body" json:"text"     max:"500"                min:"1"`
	Tag  string `default:"idea"   desc:"Category" enum:"work,private,idea" json:"tag,omitempty"`
}

type numbers struct {
	Count   int       `default:"3"    json:"count"          max:"5" min:"1"`
	Ratio   float64   `json:"ratio"   max:"1"               min:"0"`
	On      bool      `default:"true" json:"on"`
	Names   []string  `json:"names"   max:"3"               min:"1"`
	Opt     *int      `json:"opt"`
	Skipped string    `json:"-"`
	Created time.Time `json:"created"`
	When    string    `format:"date"  json:"when,omitempty"`
}

type base struct {
	ID string `json:"id"`
}

type embedded struct {
	base
	Name string `json:"name"`
}

type nested struct {
	Inner addIn             `json:"inner"`
	Items []addIn           `json:"items,omitempty"`
	Meta  map[string]string `json:"meta,omitempty"`
	Any   any               `json:"any,omitempty"`
	Raw   json.RawMessage   `json:"raw,omitempty"`
	Blob  []byte            `json:"blob,omitempty"`
}

type withURI struct {
	ID    string `uri:"id"`
	Other string `json:"other,omitempty"`
}

const keyText = "text"

const keyCount = "count"

func schemaJSON(t *testing.T, typ reflect.Type) string {
	t.Helper()
	s, err := schema.For(typ)
	require.NoError(t, err)
	b, err := json.Marshal(s)
	require.NoError(t, err)
	return string(b)
}

func TestForStruct(t *testing.T) {
	assert.JSONEq(t, `{
		"type":"object",
		"properties":{
			"text":{"type":"string","description":"Note body","minLength":1,"maxLength":500},
			"tag":{"type":"string","description":"Category","enum":["work","private","idea"],"default":"idea"}
		},
		"required":["text"]
	}`, schemaJSON(t, reflect.TypeFor[addIn]()))
}

func TestForNumbersAndOptionals(t *testing.T) {
	assert.JSONEq(t, `{
		"type":"object",
		"properties":{
			"count":{"type":"integer","minimum":1,"maximum":5,"default":3},
			"ratio":{"type":"number","minimum":0,"maximum":1},
			"on":{"type":"boolean","default":true},
			"names":{"type":"array","items":{"type":"string"},"minItems":1,"maxItems":3},
			"opt":{"type":"integer"},
			"created":{"type":"string","format":"date-time"},
			"when":{"type":"string","format":"date"}
		},
		"required":["count","ratio","on","names","created"]
	}`, schemaJSON(t, reflect.TypeFor[numbers]()))
}

func TestForEmbeddedIsFlattened(t *testing.T) {
	assert.JSONEq(t, `{
		"type":"object",
		"properties":{"id":{"type":"string"},"name":{"type":"string"}},
		"required":["id","name"]
	}`, schemaJSON(t, reflect.TypeFor[embedded]()))
}

func TestForNestedAndSpecialTypes(t *testing.T) {
	s, err := schema.For(reflect.TypeFor[nested]())
	require.NoError(t, err)
	props, ok := s["properties"].(map[string]any)
	require.True(t, ok)

	assert.Equal(t, "object", props["inner"].(map[string]any)["type"])
	assert.Equal(t, "array", props["items"].(map[string]any)["type"])
	assert.Equal(t, "object", props["meta"].(map[string]any)["type"])
	assert.Equal(t, map[string]any{"type": "string"}, props["meta"].(map[string]any)["additionalProperties"])
	assert.Equal(t, map[string]any{}, props["any"])
	assert.Equal(t, map[string]any{}, props["raw"])
	assert.Equal(t, "string", props["blob"].(map[string]any)["type"])
	assert.Equal(t, []string{"inner"}, s["required"])
}

func TestForSkipsURIFields(t *testing.T) {
	assert.JSONEq(t, `{
		"type":"object",
		"properties":{"other":{"type":"string"}}
	}`, schemaJSON(t, reflect.TypeFor[withURI]()))
}

func TestForPointerAndScalars(t *testing.T) {
	assert.JSONEq(t, `{"type":"string"}`, schemaJSON(t, reflect.TypeFor[string]()))
	assert.JSONEq(t, `{"type":"object","properties":{"text":{"type":"string","description":"Note body","minLength":1,"maxLength":500},"tag":{"type":"string","description":"Category","enum":["work","private","idea"],"default":"idea"}},"required":["text"]}`,
		schemaJSON(t, reflect.TypeFor[*addIn]()))
}

func TestForEmptyStruct(t *testing.T) {
	assert.JSONEq(t, `{"type":"object"}`, schemaJSON(t, reflect.TypeFor[struct{}]()))
}

func TestForUnsupported(t *testing.T) {
	_, err := schema.For(reflect.TypeFor[chan int]())
	require.Error(t, err)

	type bad struct {
		C chan int `json:"c"`
	}
	_, err = schema.For(reflect.TypeFor[bad]())
	require.Error(t, err)

	type badDefault struct {
		N int `default:"abc" json:"n"`
	}
	_, err = schema.For(reflect.TypeFor[badDefault]())
	require.Error(t, err)
}

func TestObjectFor(t *testing.T) {
	s, ok, err := schema.ObjectFor(reflect.TypeFor[addIn]())
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "object", s["type"])

	_, ok, err = schema.ObjectFor(reflect.TypeFor[string]())
	require.NoError(t, err)
	assert.False(t, ok)

	_, ok, err = schema.ObjectFor(reflect.TypeFor[[]addIn]())
	require.NoError(t, err)
	assert.False(t, ok)
}

type form struct {
	Name   string   `json:"name"`
	Age    int      `json:"age"   min:"1"`
	Agree  bool     `json:"agree"`
	Topics []string `enum:"a,b,c" json:"topics,omitempty"`
}

func TestForm(t *testing.T) {
	s, err := schema.Form(reflect.TypeFor[form]())
	require.NoError(t, err)
	b, err := json.Marshal(s)
	require.NoError(t, err)
	assert.JSONEq(t, `{
		"type":"object",
		"properties":{
			"name":{"type":"string"},
			"age":{"type":"integer","minimum":1},
			"agree":{"type":"boolean"},
			"topics":{"type":"array","items":{"type":"string","enum":["a","b","c"]}}
		},
		"required":["name","age","agree"]
	}`, string(b))
}

func TestFormRejectsNonFlat(t *testing.T) {
	_, err := schema.Form(reflect.TypeFor[nested]())
	require.Error(t, err)

	type multiNoEnum struct {
		Topics []string `json:"topics"`
	}
	_, err = schema.Form(reflect.TypeFor[multiNoEnum]())
	require.Error(t, err, "multi select needs an enum")

	type ints struct {
		N []int `json:"n"`
	}
	_, err = schema.Form(reflect.TypeFor[ints]())
	require.Error(t, err)

	_, err = schema.Form(reflect.TypeFor[string]())
	require.Error(t, err, "form must be a struct")
}

func TestValidate(t *testing.T) {
	s, err := schema.For(reflect.TypeFor[numbers]())
	require.NoError(t, err)

	good := map[string]any{keyCount: 2.0, "ratio": 0.5, "on": true, "names": []any{"a"}, "created": "2026-01-01T00:00:00Z"}
	require.NoError(t, schema.Validate(s, good))

	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"missing required", func(m map[string]any) { delete(m, keyCount) }},
		{"wrong type", func(m map[string]any) { m[keyCount] = "two" }},
		{"not integer", func(m map[string]any) { m[keyCount] = 2.5 }},
		{"below minimum", func(m map[string]any) { m[keyCount] = 0.0 }},
		{"above maximum", func(m map[string]any) { m["ratio"] = 2.0 }},
		{"bad bool", func(m map[string]any) { m["on"] = "yes" }},
		{"array too short", func(m map[string]any) { m["names"] = []any{} }},
		{"array too long", func(m map[string]any) { m["names"] = []any{"a", "b", "c", "d"} }},
		{"bad item type", func(m map[string]any) { m["names"] = []any{1.0} }},
		{"not an array", func(m map[string]any) { m["names"] = "a" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := map[string]any{}
			maps.Copy(m, good)
			tt.mutate(m)
			require.Error(t, schema.Validate(s, m))
		})
	}

	require.Error(t, schema.Validate(s, "not an object"))
}

func TestValidateStringsAndEnum(t *testing.T) {
	s, err := schema.For(reflect.TypeFor[addIn]())
	require.NoError(t, err)

	require.NoError(t, schema.Validate(s, map[string]any{keyText: "hi"}))
	require.NoError(t, schema.Validate(s, map[string]any{keyText: "hi", "tag": "work"}))
	require.Error(t, schema.Validate(s, map[string]any{keyText: ""}), "minLength")
	require.Error(t, schema.Validate(s, map[string]any{keyText: "hi", "tag": "other"}), "enum")

	long := make([]byte, 501)
	for i := range long {
		long[i] = 'a'
	}
	require.Error(t, schema.Validate(s, map[string]any{keyText: string(long)}), "maxLength")
}

func TestValidateNestedPathInError(t *testing.T) {
	s, err := schema.For(reflect.TypeFor[nested]())
	require.NoError(t, err)
	err = schema.Validate(s, map[string]any{"inner": map[string]any{keyText: ""}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "inner.text")
}

type uriParams struct {
	ID    string `uri:"id"`
	Count int    `uri:"count"`
	Skip  string
}

func TestBindURI(t *testing.T) {
	var p uriParams
	require.NoError(t, schema.BindURI(map[string]string{"id": "note-1", keyCount: "7"}, &p))
	assert.Equal(t, "note-1", p.ID)
	assert.Equal(t, 7, p.Count)
	assert.Empty(t, p.Skip)

	require.Error(t, schema.BindURI(map[string]string{keyCount: "x"}, &p))
	require.Error(t, schema.BindURI(map[string]string{}, p), "needs a pointer")

	type unsupported struct {
		M map[string]string `uri:"m"`
	}
	require.Error(t, schema.BindURI(map[string]string{"m": "x"}, &unsupported{}))
}

func TestURIVars(t *testing.T) {
	assert.Equal(t, []string{"id", keyCount}, schema.URIVars(reflect.TypeFor[uriParams]()))
	assert.Empty(t, schema.URIVars(reflect.TypeFor[addIn]()))
	assert.Empty(t, schema.URIVars(reflect.TypeFor[string]()))
}

type withHeaders struct {
	Region string `header:"Region"  json:"region"`
	Count  int    `header:"X-Count" json:"count"`
	On     bool   `header:"On"      json:"on,omitempty"`
	Query  string `json:"query"`
}

func TestHeaderTag(t *testing.T) {
	s, err := schema.For(reflect.TypeFor[withHeaders]())
	require.NoError(t, err)
	props, _ := s["properties"].(map[string]any)
	region, _ := props["region"].(map[string]any)
	assert.Equal(t, "Region", region["x-mcp-header"])
	query, _ := props["query"].(map[string]any)
	assert.NotContains(t, query, "x-mcp-header")

	assert.Equal(t, []schema.HeaderParam{
		{Header: "On", Prop: "on", Type: "boolean"},
		{Header: "Region", Prop: "region", Type: "string"},
		{Header: "X-Count", Prop: keyCount, Type: "integer"},
	}, schema.HeaderParams(s))
	assert.Empty(t, schema.HeaderParams(map[string]any{"type": "object"}))
}

func TestHeaderTagErrors(t *testing.T) {
	type number struct {
		N float64 `header:"N" json:"n"`
	}
	type empty struct {
		S string `header:"" json:"s"`
	}
	type badChars struct {
		S string `header:"has space" json:"s"` //nolint:tagliatelle // invalid on purpose
	}
	type duplicate struct {
		A string `header:"Same" json:"a"`
		B string `header:"same" json:"b"` //nolint:tagliatelle // duplicate on purpose
	}
	type inner struct {
		S string `header:"Deep" json:"s"`
	}
	type nested struct {
		In inner `json:"in"`
	}
	type list struct {
		L []string `header:"L" json:"l"`
	}
	for name, typ := range map[string]reflect.Type{
		"number": reflect.TypeFor[number](), "empty": reflect.TypeFor[empty](), "bad chars": reflect.TypeFor[badChars](),
		"duplicate": reflect.TypeFor[duplicate](), "nested": reflect.TypeFor[nested](), "list": reflect.TypeFor[list](),
	} {
		_, err := schema.For(typ)
		require.Error(t, err, name)
	}
}
