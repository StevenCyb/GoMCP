package caller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/StevenCyb/GoMCP/internal/runner"
)

// Request is what Run does: list the tools when Tool is empty, otherwise call it.
type Request struct {
	Tool string
	Args json.RawMessage // JSON object, empty for none
	JSON bool            // print the raw result as indented JSON instead of the text
}

// Config says which server to talk to and how to sign in.
type Config struct {
	// Target is the URL of a remote server, or the directory of a project that is built and run over stdio.
	Target string
	// Tags and DotEnv are the build tags and the .env file of a project.
	Tags, DotEnv string
	// Roots are the directories answered to roots/list.
	Roots []string
	// Token is a fixed bearer token, OAuth logs in with the browser; both are for remote servers.
	Token string
	OAuth bool
	// ClientID and Scope are for OAuth, both are optional.
	ClientID, Scope string
	// CallbackPort is the port of the OAuth redirect address, 0 picks a free one.
	CallbackPort int
	// Stderr gets the build and server logs and the login URL; nil drops them.
	Stderr io.Writer
	// OpenURL and CacheFile replace the browser and the token file, tests do.
	OpenURL   func(string) error
	CacheFile string
}

// IsURL tells a remote server from a project directory.
func IsURL(target string) bool {
	return strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://")
}

// ParseArgs checks that args is empty or a JSON object.
func ParseArgs(args string) (json.RawMessage, error) {
	if args == "" {
		return nil, nil
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(args), &obj); err != nil || obj == nil {
		return nil, errors.New("the arguments must be a JSON object, for example '{\"text\":\"hi\"}'")
	}
	return json.RawMessage(args), nil
}

// Connect returns a client for the target and a function that ends the session. For a project it builds
// the project and starts it over stdio; ending closes its stdin, which stops the server.
func Connect(ctx context.Context, cfg Config) (*Client, func() error, error) {
	if cfg.Stderr == nil {
		cfg.Stderr = io.Discard
	}
	if IsURL(cfg.Target) {
		var auth *Auth
		if cfg.Token != "" || cfg.OAuth {
			auth = &Auth{Token: cfg.Token, OAuth: cfg.OAuth, ClientID: cfg.ClientID, Scope: cfg.Scope, CallbackPort: cfg.CallbackPort,
				Open: cfg.OpenURL, Log: cfg.Stderr, CacheFile: cfg.CacheFile}
		}
		return NewClient(NewHTTP(ctx, cfg.Target, auth), cfg.Roots), func() error { return nil }, nil
	}
	if cfg.Token != "" || cfg.OAuth {
		return nil, nil, errors.New("--token and --oauth are for remote servers: a project runs over stdio without authentication")
	}
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	o := runner.Options{Dir: cfg.Target, Tags: cfg.Tags, DotEnv: cfg.DotEnv, Stdio: true, Stdin: inR, Stdout: outW, Stderr: cfg.Stderr}
	done := make(chan error, 1)
	go func() {
		err := runner.Serve(ctx, o)
		_ = outW.Close()
		_ = inR.Close()
		done <- err
	}()
	end := func() error {
		_ = inW.Close() // end of input stops the server
		_ = outR.Close()
		return <-done
	}
	return New(inW, outR, cfg.Roots), end, nil
}

// Run connects, performs the request, prints the outcome to stdout and ends the session.
func Run(ctx context.Context, cfg Config, req Request, stdout io.Writer) error {
	c, end, err := Connect(ctx, cfg)
	if err != nil {
		return err
	}
	err = talk(c, req, stdout)
	if serr := end(); serr != nil {
		var rpc *RPCError
		var tool *ToolError
		var input *InputError
		var auth *AuthError
		if !errors.As(err, &rpc) && !errors.As(err, &tool) && !errors.As(err, &input) && !errors.As(err, &auth) {
			err = serr // the server did not start or died: that is the cause of any pipe error
		}
	}
	return err
}

func talk(c *Client, req Request, stdout io.Writer) error {
	if req.Tool == "" {
		tools, err := c.Tools()
		if err != nil {
			return err
		}
		_, err = io.WriteString(stdout, FormatTools(tools))
		return err
	}
	raw, err := c.Call(req.Tool, req.Args)
	if raw != nil && (req.JSON || err == nil) { // a tool error is printed once, by the caller of Run
		out := Text(raw)
		if req.JSON {
			out = Indent(raw)
		}
		if _, werr := fmt.Fprintln(stdout, out); werr != nil {
			return werr
		}
	}
	return err
}
