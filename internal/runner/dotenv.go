package runner

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
)

// ParseDotEnv parses the content of a .env file: KEY=value lines, optional export prefix,
// # comments, and single or double quoted values (double quotes understand \n, \t, \" and \\).
func ParseDotEnv(content string) (map[string]string, error) {
	out := map[string]string{}
	for i, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		key, value, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			continue
		}
		parsed, err := parseValue(strings.TrimSpace(value))
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", i+1, err)
		}
		out[key] = parsed
	}
	return out, nil
}

var errUnterminated = errors.New("unterminated quote")

func parseValue(v string) (string, error) {
	switch {
	case strings.HasPrefix(v, `"`):
		var sb strings.Builder
		for i := 1; i < len(v); i++ {
			switch c := v[i]; c {
			case '"':
				return sb.String(), nil
			case '\\':
				i++
				if i >= len(v) {
					return "", errUnterminated
				}
				switch v[i] {
				case 'n':
					sb.WriteByte('\n')
				case 't':
					sb.WriteByte('\t')
				default:
					sb.WriteByte(v[i])
				}
			default:
				sb.WriteByte(c)
			}
		}
		return "", errUnterminated
	case strings.HasPrefix(v, "'"):
		end := strings.Index(v[1:], "'")
		if end < 0 {
			return "", errUnterminated
		}
		return v[1 : 1+end], nil
	default:
		if i := strings.Index(v, " #"); i >= 0 {
			v = v[:i]
		}
		return strings.TrimSpace(v), nil
	}
}

// LoadDotEnv adds the variables of the .env file at path to env. Variables that are already
// set in env win. A missing file is not an error.
func LoadDotEnv(path string, env []string) ([]string, error) {
	data, err := os.ReadFile(path) //nolint:gosec // the path is chosen by the user
	if errors.Is(err, os.ErrNotExist) {
		return env, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	vars, err := ParseDotEnv(string(data))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	set := make(map[string]bool, len(env))
	for _, e := range env {
		key, _, _ := strings.Cut(e, "=")
		set[key] = true
	}
	keys := slices.Sorted(maps.Keys(vars))
	out := append([]string(nil), env...)
	for _, k := range keys {
		if !set[k] {
			out = append(out, k+"="+vars[k])
		}
	}
	return out, nil
}
