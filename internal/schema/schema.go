// Package schema derives JSON Schemas from Go types and validates decoded JSON against them.
//
// Struct tags: json (name, omitempty), desc, enum, min, max, default, format, uri and header.
// A field is required unless it has omitempty or is a pointer.
package schema

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	keyType     = "type"
	typeString  = "string"
	typeInteger = "integer"
	typeNumber  = "number"
	typeBoolean = "boolean"
	typeArray   = "array"
	typeObject  = "object"
)

func isTime(t reflect.Type) bool       { return t == reflect.TypeFor[time.Time]() }
func isRawMessage(t reflect.Type) bool { return t == reflect.TypeFor[json.RawMessage]() }

// field is one struct field that takes part in the schema.
type field struct {
	name     string
	typ      reflect.Type
	tag      reflect.StructTag
	optional bool
}

// For returns the JSON Schema of t.
func For(t reflect.Type) (map[string]any, error) {
	return build(t, "", true)
}

// ObjectFor returns the schema of t if t is a struct; MCP output schemas must be objects.
func ObjectFor(t reflect.Type) (map[string]any, bool, error) {
	t = deref(t)
	if t.Kind() != reflect.Struct || isTime(t) {
		return nil, false, nil
	}
	s, err := build(t, "", true)
	return s, err == nil, err
}

// Form returns the schema of t for an elicitation form: a flat struct of primitives,
// where lists are only allowed as multi select (strings with enum).
func Form(t reflect.Type) (map[string]any, error) {
	t = deref(t)
	if t.Kind() != reflect.Struct {
		return nil, fmt.Errorf("schema: elicitation form must be a struct, got %s", t)
	}
	s, err := build(t, "", true)
	if err != nil {
		return nil, err
	}
	props, _ := s["properties"].(map[string]any)
	for name, raw := range props {
		if err := checkFormProperty(name, raw); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func checkFormProperty(name string, raw any) error {
	prop, _ := raw.(map[string]any)
	switch prop[keyType] {
	case typeString, typeInteger, typeNumber, typeBoolean:
		return nil
	case typeArray:
		items, _ := prop["items"].(map[string]any)
		if items[keyType] == typeString && items["enum"] != nil {
			return nil
		}
		return fmt.Errorf("schema: form field %q: lists need string items with enum", name)
	default:
		return fmt.Errorf("schema: form field %q: forms must be flat", name)
	}
}

func deref(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t
}

func build(t reflect.Type, tag reflect.StructTag, root bool) (map[string]any, error) {
	t = deref(t)

	var (
		s   map[string]any
		err error
	)
	switch {
	case isTime(t):
		s = map[string]any{keyType: typeString, "format": "date-time"}
	case isRawMessage(t):
		s = map[string]any{}
	case t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Uint8:
		s = map[string]any{keyType: typeString, "contentEncoding": "base64"}
	default:
		s, err = buildKind(t, tag, root)
	}
	if err != nil {
		return nil, err
	}
	return s, applyTag(s, tag)
}

func buildKind(t reflect.Type, tag reflect.StructTag, root bool) (map[string]any, error) {
	//nolint:exhaustive // unsupported kinds fall through to the default error
	switch t.Kind() {
	case reflect.String:
		return map[string]any{keyType: typeString}, nil
	case reflect.Bool:
		return map[string]any{keyType: typeBoolean}, nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{keyType: typeInteger}, nil
	case reflect.Float32, reflect.Float64:
		return map[string]any{keyType: typeNumber}, nil
	case reflect.Interface:
		return map[string]any{}, nil
	case reflect.Slice, reflect.Array:
		items, err := build(t.Elem(), reflect.StructTag(itemTag(tag)), false)
		if err != nil {
			return nil, err
		}
		return map[string]any{keyType: typeArray, "items": items}, nil
	case reflect.Map:
		if t.Key().Kind() != reflect.String {
			return nil, fmt.Errorf("schema: map key of %s must be a string", t)
		}
		values, err := build(t.Elem(), "", false)
		if err != nil {
			return nil, err
		}
		return map[string]any{keyType: typeObject, "additionalProperties": values}, nil
	case reflect.Struct:
		return buildStruct(t, root)
	default:
		return nil, fmt.Errorf("schema: unsupported type %s", t)
	}
}

// itemTag passes enum down to list items (multi select), the other tags describe the list itself.
func itemTag(tag reflect.StructTag) string {
	if enum, ok := tag.Lookup("enum"); ok {
		return `enum:"` + enum + `"`
	}
	return ""
}

func buildStruct(t reflect.Type, root bool) (map[string]any, error) {
	s := map[string]any{keyType: typeObject}
	props := map[string]any{}
	headers := map[string]bool{}
	var required []string

	for _, f := range fields(t) {
		prop, err := build(f.typ, f.tag, false)
		if err != nil {
			return nil, fmt.Errorf("field %q: %w", f.name, err)
		}
		if h, has := f.tag.Lookup("header"); has {
			if err := addHeader(prop, h, root, headers); err != nil {
				return nil, fmt.Errorf("field %q: %w", f.name, err)
			}
		}
		props[f.name] = prop
		if !f.optional {
			required = append(required, f.name)
		}
	}
	if len(props) > 0 {
		s["properties"] = props
	}
	if len(required) > 0 {
		s["required"] = required
	}
	return s, nil
}

// fields lists the schema fields of a struct, flattening embedded structs and skipping uri fields.
func fields(t reflect.Type) []field {
	var out []field
	for sf := range t.Fields() {
		jsonTag, hasJSON := sf.Tag.Lookup("json")
		if jsonTag == "-" {
			continue
		}
		if _, isURI := sf.Tag.Lookup("uri"); isURI {
			continue
		}
		name, opts, _ := strings.Cut(jsonTag, ",")
		if sf.Anonymous && (!hasJSON || name == "") && deref(sf.Type).Kind() == reflect.Struct {
			out = append(out, fields(deref(sf.Type))...)
			continue
		}
		if !sf.IsExported() {
			continue
		}
		if name == "" {
			name = sf.Name
		}
		out = append(out, field{
			name:     name,
			typ:      sf.Type,
			tag:      sf.Tag,
			optional: strings.Contains(opts, "omitempty") || sf.Type.Kind() == reflect.Pointer,
		})
	}
	return out
}

// applyTag adds the desc, enum, min, max, default and format constraints to s.
func applyTag(s map[string]any, tag reflect.StructTag) error {
	if d := tag.Get("desc"); d != "" {
		s["description"] = d
	}
	if f := tag.Get("format"); f != "" {
		s["format"] = f
	}
	typ, _ := s[keyType].(string)
	if enum := tag.Get("enum"); enum != "" && typ != typeArray {
		s["enum"] = strings.Split(enum, ",")
	}
	if err := applyBounds(s, typ, tag); err != nil {
		return err
	}
	if def, ok := tag.Lookup("default"); ok {
		v, err := parseTyped(typ, def)
		if err != nil {
			return fmt.Errorf("default %q: %w", def, err)
		}
		s["default"] = v
	}
	return nil
}

func applyBounds(s map[string]any, typ string, tag reflect.StructTag) error {
	keys := map[string][2]string{
		typeString:  {"minLength", "maxLength"},
		typeInteger: {"minimum", "maximum"},
		typeNumber:  {"minimum", "maximum"},
		typeArray:   {"minItems", "maxItems"},
	}
	names, ok := keys[typ]
	if !ok {
		return nil
	}
	for i, tagName := range []string{"min", "max"} {
		raw, has := tag.Lookup(tagName)
		if !has {
			continue
		}
		bound := typeInteger
		if typ == typeNumber {
			bound = typeNumber
		}
		v, err := parseTyped(bound, raw)
		if err != nil {
			return fmt.Errorf("%s %q: %w", tagName, raw, err)
		}
		s[names[i]] = v
	}
	return nil
}

func parseTyped(typ, raw string) (any, error) {
	switch typ {
	case typeBoolean:
		return strconv.ParseBool(raw)
	case typeInteger:
		return strconv.ParseInt(raw, 10, 64)
	case typeNumber:
		return strconv.ParseFloat(raw, 64)
	default:
		return raw, nil
	}
}

// Validate checks a decoded JSON value (as produced by json.Unmarshal into any) against s.
// It covers type, required, enum and the min/max bounds, and reports the first violation.
func Validate(s map[string]any, v any) error {
	return validate(s, v, "")
}

func join(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

func validate(s map[string]any, v any, path string) error {
	typ, _ := s[keyType].(string)
	switch typ {
	case typeObject:
		return validateObject(s, v, path)
	case typeArray:
		return validateArray(s, v, path)
	case typeString:
		str, ok := v.(string)
		if !ok {
			return typeError(path, typeString, v)
		}
		return validateString(s, str, path)
	case typeInteger, typeNumber:
		n, ok := v.(float64)
		if !ok {
			return typeError(path, typ, v)
		}
		return validateNumber(s, n, typ == typeInteger, path)
	case typeBoolean:
		if _, ok := v.(bool); !ok {
			return typeError(path, typeBoolean, v)
		}
	}
	return nil
}

func typeError(path, want string, got any) error {
	return fmt.Errorf("%s: expected %s, got %T", pathOrRoot(path), want, got)
}

func pathOrRoot(path string) string {
	if path == "" {
		return "value"
	}
	return path
}

func validateObject(s map[string]any, v any, path string) error {
	obj, ok := v.(map[string]any)
	if !ok {
		return typeError(path, typeObject, v)
	}
	required, _ := s["required"].([]string)
	for _, name := range required {
		if val, present := obj[name]; !present || val == nil {
			return fmt.Errorf("%s: missing required field", join(path, name))
		}
	}
	props, _ := s["properties"].(map[string]any)
	for name, raw := range props {
		val, present := obj[name]
		if !present || val == nil {
			continue
		}
		prop, _ := raw.(map[string]any)
		if err := validate(prop, val, join(path, name)); err != nil {
			return err
		}
	}
	if extra, ok := s["additionalProperties"].(map[string]any); ok {
		for name, val := range obj {
			if err := validate(extra, val, join(path, name)); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateArray(s map[string]any, v any, path string) error {
	arr, ok := v.([]any)
	if !ok {
		return typeError(path, typeArray, v)
	}
	if n, ok := toFloat(s["minItems"]); ok && float64(len(arr)) < n {
		return fmt.Errorf("%s: %d items, minimum is %v", pathOrRoot(path), len(arr), n)
	}
	if n, ok := toFloat(s["maxItems"]); ok && float64(len(arr)) > n {
		return fmt.Errorf("%s: %d items, maximum is %v", pathOrRoot(path), len(arr), n)
	}
	items, _ := s["items"].(map[string]any)
	for i, el := range arr {
		if err := validate(items, el, fmt.Sprintf("%s[%d]", pathOrRoot(path), i)); err != nil {
			return err
		}
	}
	return nil
}

func validateString(s map[string]any, str, path string) error {
	length := float64(len([]rune(str)))
	if n, ok := toFloat(s["minLength"]); ok && length < n {
		return fmt.Errorf("%s: length %v below minimum %v", pathOrRoot(path), length, n)
	}
	if n, ok := toFloat(s["maxLength"]); ok && length > n {
		return fmt.Errorf("%s: length %v above maximum %v", pathOrRoot(path), length, n)
	}
	if enum, ok := s["enum"].([]string); ok {
		if slices.Contains(enum, str) {
			return nil
		}
		return fmt.Errorf("%s: %q is not one of %s", pathOrRoot(path), str, strings.Join(enum, ", "))
	}
	return nil
}

func validateNumber(s map[string]any, n float64, integer bool, path string) error {
	if integer && n != float64(int64(n)) {
		return fmt.Errorf("%s: %v is not an integer", pathOrRoot(path), n)
	}
	if lo, ok := toFloat(s["minimum"]); ok && n < lo {
		return fmt.Errorf("%s: %v below minimum %v", pathOrRoot(path), n, lo)
	}
	if hi, ok := toFloat(s["maximum"]); ok && n > hi {
		return fmt.Errorf("%s: %v above maximum %v", pathOrRoot(path), n, hi)
	}
	return nil
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case int64:
		return float64(n), true
	case float64:
		return n, true
	default:
		return 0, false
	}
}

// URIVars lists the uri tag names of a struct in field order.
func URIVars(t reflect.Type) []string {
	t = deref(t)
	if t.Kind() != reflect.Struct {
		return nil
	}
	var out []string
	for field := range t.Fields() {
		if name, ok := field.Tag.Lookup("uri"); ok {
			out = append(out, name)
		}
	}
	return out
}

// BindURI copies URI template variables into the uri tagged fields of the struct dst points to.
func BindURI(vars map[string]string, dst any) error {
	rv := reflect.ValueOf(dst)
	if rv.Kind() != reflect.Pointer || rv.Elem().Kind() != reflect.Struct {
		return errors.New("schema: BindURI needs a pointer to a struct")
	}
	rv = rv.Elem()
	for i := range rv.NumField() {
		name, ok := rv.Type().Field(i).Tag.Lookup("uri")
		if !ok {
			continue
		}
		raw, present := vars[name]
		if !present {
			continue
		}
		if err := setFromString(rv.Field(i), raw); err != nil {
			return fmt.Errorf("uri variable %q: %w", name, err)
		}
	}
	return nil
}

func setFromString(v reflect.Value, raw string) error {
	//nolint:exhaustive // unsupported kinds fall through to the default error
	switch v.Kind() {
	case reflect.String:
		v.SetString(raw)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := strconv.ParseInt(raw, 10, v.Type().Bits())
		if err != nil {
			return err
		}
		v.SetInt(n)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n, err := strconv.ParseUint(raw, 10, v.Type().Bits())
		if err != nil {
			return err
		}
		v.SetUint(n)
	case reflect.Bool:
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return err
		}
		v.SetBool(b)
	default:
		return fmt.Errorf("unsupported type %s", v.Type())
	}
	return nil
}

// KeyHeader is the schema keyword that makes a tool argument also travel as an HTTP header.
const KeyHeader = "x-mcp-header"

// addHeader validates the header tag of a field and puts it into the property schema.
func addHeader(prop map[string]any, name string, root bool, seen map[string]bool) error {
	switch {
	case !root:
		return errors.New("header is only allowed on top-level fields")
	case name == "" || strings.IndexFunc(name, func(r rune) bool { return !isTokenChar(r) }) >= 0:
		return fmt.Errorf("header %q must be a non-empty HTTP token", name)
	case seen[strings.ToLower(name)]:
		return fmt.Errorf("header %q is used twice", name)
	}
	switch prop[keyType] {
	case typeString, typeInteger, typeBoolean:
	default:
		return fmt.Errorf("header %q needs a string, integer or boolean field", name)
	}
	seen[strings.ToLower(name)] = true
	prop[KeyHeader] = name
	return nil
}

func isTokenChar(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", r)
}

// HeaderParam is a top-level property that also travels as the HTTP header Mcp-Param-<Header>.
type HeaderParam struct {
	Header string
	Prop   string
	Type   string
}

// HeaderParams lists the properties of an object schema that have an x-mcp-header.
func HeaderParams(s map[string]any) []HeaderParam {
	props, _ := s["properties"].(map[string]any)
	var out []HeaderParam
	for name, raw := range props {
		prop, _ := raw.(map[string]any)
		if h, ok := prop[KeyHeader].(string); ok {
			typ, _ := prop[keyType].(string)
			out = append(out, HeaderParam{Header: h, Prop: name, Type: typ})
		}
	}
	slices.SortFunc(out, func(a, b HeaderParam) int { return strings.Compare(a.Header, b.Header) })
	return out
}
