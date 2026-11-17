package main

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/StevenCyb/GoMCP/internal/caller"
)

const optJSON, optRoot = "json", "root"

// callInput is the command line of "gomcp call". The command line library takes the arguments in a
// fixed order and cannot make the tool and its arguments optional, so call parses its own line.
type callInput struct {
	project, tool, args string
	tags, dotenv        string
	roots               []string
	json                bool
}

// parseCall reads "gomcp call <project> [tool [arguments]] [flags]" and returns the command line the
// command line library sees: the call command with only the project. A help flag or a missing project
// is left to the library.
func parseCall(args []string, in *callInput) ([]string, error) {
	if len(args) < 2 || args[1] != "call" {
		return args, nil
	}
	var positional []string
	for i := 2; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-h" || a == "--help":
			return args, nil
		case a == "--"+optJSON:
			in.json = true
		case a == "--"+optTags || a == "-t" || a == "--"+optDotEnv || a == "-e" || a == "--"+optRoot || a == "-r":
			i++
			if i >= len(args) {
				return nil, fmt.Errorf("%s needs a value", a)
			}
			switch a {
			case "--" + optTags, "-t":
				in.tags = args[i]
			case "--" + optDotEnv, "-e":
				in.dotenv = args[i]
			default:
				in.roots = append(in.roots, args[i])
			}
		case strings.HasPrefix(a, "-") && a != "-":
			return nil, fmt.Errorf("unknown flag %s", a)
		default:
			positional = append(positional, a)
		}
	}
	if len(positional) == 0 {
		return args[:2], nil // no project: the library reports the usage
	}
	if len(positional) > 3 {
		return nil, errors.New("call takes a project path, a tool name and a JSON object")
	}
	in.project = positional[0]
	if len(positional) > 1 {
		in.tool = positional[1]
	}
	if len(positional) > 2 {
		in.args = positional[2]
	}
	return []string{args[0], "call", in.project}, nil
}

// resolve checks the arguments before anything is built: a JSON object, or "-" for stdin.
func (in *callInput) resolve(stdin io.Reader) (caller.Request, error) {
	text := in.args
	if text == "-" {
		raw, err := io.ReadAll(stdin)
		if err != nil {
			return caller.Request{}, fmt.Errorf("read arguments from stdin: %w", err)
		}
		text = strings.TrimSpace(string(raw))
	}
	parsed, err := caller.ParseArgs(text)
	if err != nil {
		return caller.Request{}, err
	}
	return caller.Request{Tool: in.tool, Args: parsed, JSON: in.json, Roots: in.roots}, nil
}
