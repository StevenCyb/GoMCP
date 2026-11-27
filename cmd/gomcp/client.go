package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/StevenCyb/GoMCP/internal/caller"
)

const (
	optJSON, optRoot, optToken, optOAuth = "json", "root", "token", "oauth"
	optClientID, optScope, optPort       = "client-id", "scope", "callback-port"
	subList, subCall, subInteractive     = "list", "call", "interactive"
	envToken                             = "GOMCP_TOKEN"
)

// clientInput is the command line of "gomcp client". The command line library takes the arguments in a
// fixed order and cannot make the tool and its arguments optional, so client parses its own line.
type clientInput struct {
	sub, target, tool, args string
	tags, dotenv            string
	token, clientID, scope  string
	roots                   []string
	port                    int
	json, oauth             bool
}

// parseClient reads "gomcp client list|call|interactive <target> [tool [arguments]] [flags]" and returns
// the command line the command line library sees: the subcommand with only the target. A help flag, a
// missing subcommand or a missing target is left to the library.
func parseClient(args []string, in *clientInput) ([]string, error) {
	if len(args) < 3 || args[1] != "client" {
		return args, nil
	}
	switch args[2] {
	case subList, subCall, subInteractive:
		in.sub = args[2]
	default:
		return args, nil
	}
	var positional []string
	for i := 3; i < len(args); i++ {
		a := args[i]
		if a == "-h" || a == "--help" {
			return args, nil
		}
		if !strings.HasPrefix(a, "-") || a == "-" {
			positional = append(positional, a)
			continue
		}
		name, value, hasValue := strings.Cut(a, "=")
		switch name {
		case "--" + optJSON:
			in.json = true
			continue
		case "--" + optOAuth:
			in.oauth = true
			continue
		}
		if !in.knownFlag(name) {
			return nil, fmt.Errorf("unknown flag %s", name)
		}
		if !hasValue {
			if i++; i >= len(args) {
				return nil, fmt.Errorf("%s needs a value", name)
			}
			value = args[i]
		}
		if err := in.setFlag(name, value); err != nil {
			return nil, err
		}
	}
	if len(positional) == 0 {
		return args[:3], nil // no target: the library reports the usage
	}
	limit := map[string]int{subList: 1, subInteractive: 1, subCall: 3}[in.sub]
	if len(positional) > limit {
		return nil, fmt.Errorf("%s takes %s", in.sub, map[string]string{
			subList: "a target", subInteractive: "a target", subCall: "a target, a tool name and a JSON object",
		}[in.sub])
	}
	in.target = positional[0]
	if len(positional) > 1 {
		in.tool = positional[1]
	}
	if len(positional) > 2 {
		in.args = positional[2]
	}
	if in.sub == subCall && in.tool == "" {
		return nil, errors.New("call needs a tool name, list shows them")
	}
	return []string{args[0], "client", in.sub, in.target}, nil
}

// config is the connection the flags describe.
func (in *clientInput) config(stderr io.Writer) caller.Config {
	token := in.token
	if token == "" {
		token = os.Getenv(envToken)
	}
	return caller.Config{
		Target: in.target, Tags: in.tags, DotEnv: in.dotenv, Roots: in.roots,
		Token: token, OAuth: in.oauth, ClientID: in.clientID, Scope: in.scope, CallbackPort: in.port, Stderr: stderr,
	}
}

// resolve checks the arguments before anything is built or connected: a JSON object, or "-" for stdin.
func (in *clientInput) resolve(stdin io.Reader) (caller.Request, error) {
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
	return caller.Request{Tool: in.tool, Args: parsed, JSON: in.json}, nil
}

func (in *clientInput) knownFlag(name string) bool {
	switch name {
	case "--" + optTags, "-t", "--" + optDotEnv, "-e", "--" + optToken, "--" + optClientID, "--" + optScope, "--" + optRoot, "-r", "--" + optPort:
		return true
	}
	return false
}

// setFlag stores the value of a flag that takes one.
func (in *clientInput) setFlag(name, value string) error {
	switch name {
	case "--" + optTags, "-t":
		in.tags = value
	case "--" + optDotEnv, "-e":
		in.dotenv = value
	case "--" + optToken:
		in.token = value
	case "--" + optClientID:
		in.clientID = value
	case "--" + optScope:
		in.scope = value
	case "--" + optRoot, "-r":
		in.roots = append(in.roots, value)
	case "--" + optPort:
		p, err := strconv.Atoi(value)
		if err != nil || p < 1 || p > 65535 {
			return fmt.Errorf("%s needs a port number", name)
		}
		in.port = p
	}
	return nil
}
