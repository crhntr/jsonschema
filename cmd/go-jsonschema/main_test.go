package main

import (
	"bytes"
	"fmt"
	"testing"

	"rsc.io/script"
	"rsc.io/script/scripttest"
)

func Test(t *testing.T) {
	e := script.NewEngine()

	e.Cmds["go-jsonschema"] = script.Command(script.CmdUsage{
		Summary: "go-jsonschema",
		Args:    "",
	}, func(state *script.State, args ...string) (script.WaitFunc, error) {
		return func(state *script.State) (string, string, error) {
			var stdout, stderr bytes.Buffer
			code := run(state.Getwd(), args, &stdout, &stderr)
			if code != 0 {
				return stdout.String(), stderr.String(), ExitCode(code)
			}
			return stdout.String(), stderr.String(), nil
		}, nil
	})

	scripttest.Test(t, t.Context(), e, nil, "testdata/*.txt")
}

type ExitCode int

func (ec ExitCode) Error() string {
	return fmt.Sprintf("exit %d", ec)
}
