package mcp

import (
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContentConstructors(t *testing.T) {
	assert.Equal(t, Content{Type: "text", Text: "hi"}, Text("hi"))

	link := ResourceLink("note:///1")
	assert.Equal(t, "resource_link", link.Type)
	assert.Equal(t, "note:///1", link.URI)

	emb := EmbedResource("note:///1")
	assert.Equal(t, "resource", emb.Type)
	assert.Equal(t, "note:///1", emb.embed)
}

func TestResultImplementsResultValue(t *testing.T) {
	var v resultValue = Result[string]{Structured: "x", Content: []Content{Text("t")}}
	assert.Equal(t, "x", v.structured())
	assert.Len(t, v.extra(), 1)
}

func TestContents(t *testing.T) {
	assert.Equal(t, "a", TextContent("a").Text)

	c, err := JSONContents(map[string]int{"a": 1})
	require.NoError(t, err)
	require.Len(t, c, 1)
	assert.JSONEq(t, `{"a":1}`, c[0].Text)
	assert.Equal(t, "application/json", c[0].MIMEType)

	_, err = JSONContents(make(chan int))
	require.Error(t, err)
}

func TestMessageHelpers(t *testing.T) {
	assert.Equal(t, RoleUser, UserText("a").Role)
	assert.Equal(t, RoleUser, User(Text("a")).Role)
	assert.Equal(t, SampleMsg{Role: RoleUser, Content: Text("q")}, SampleText(RoleUser, "q"))
}

func TestPaginate(t *testing.T) {
	items := []int{1, 2, 3, 4, 5}

	all := Paginate(items, PageReq{})
	assert.Equal(t, items, all.Items)
	assert.Empty(t, all.Next)

	first := Paginate(items, PageReq{Size: 2})
	assert.Equal(t, []int{1, 2}, first.Items)
	require.NotEmpty(t, first.Next)

	second := Paginate(items, PageReq{Size: 2, Cursor: first.Next})
	assert.Equal(t, []int{3, 4}, second.Items)

	last := Paginate(items, PageReq{Size: 2, Cursor: second.Next})
	assert.Equal(t, []int{5}, last.Items)
	assert.Empty(t, last.Next)

	bad := Paginate(items, PageReq{Size: 2, Cursor: "!!!"})
	assert.Equal(t, []int{1, 2}, bad.Items, "invalid cursor restarts at the beginning")

	beyond := Paginate(items, PageReq{Size: 2, Cursor: base64.RawURLEncoding.EncodeToString([]byte("99"))})
	assert.Empty(t, beyond.Items)
}
