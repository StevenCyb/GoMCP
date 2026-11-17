package caller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/StevenCyb/GoMCP/internal/runner"
)

// Request is what Run does: list the tools when Tool is empty, otherwise call it.
type Request struct {
	Tool  string
	Args  json.RawMessage // JSON object, empty for none
	JSON  bool            // print the raw result as indented JSON instead of the text
	Roots []string        // directories answered to roots/list
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

// Run builds the project, starts it over stdio, performs the request, prints the outcome to stdout and
// ends the server by closing its stdin. o.Stdout and o.Stdin are replaced, o.Stderr gets the logs.
func Run(ctx context.Context, o runner.Options, req Request, stdout io.Writer) error {
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	o.Stdio, o.Stdin, o.Stdout = true, inR, outW
	done := make(chan error, 1)
	go func() {
		err := runner.Serve(ctx, o)
		_ = outW.Close()
		_ = inR.Close()
		done <- err
	}()

	err := talk(New(inW, outR, req.Roots), req, stdout)
	_ = inW.Close() // end of input stops the server
	_ = outR.Close()
	if serr := <-done; serr != nil {
		var rpc *RPCError
		var tool *ToolError
		var input *InputError
		if !errors.As(err, &rpc) && !errors.As(err, &tool) && !errors.As(err, &input) {
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
