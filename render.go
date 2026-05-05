// Command render applies go-makefile bootstrap templates to JSON context
// from stdin.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"text/template"
)

const expectedArgCount = 2

type context struct {
	Binary  string `json:"Binary"`
	Cmd     string `json:"Cmd"`
	Layout  string `json:"Layout"`
	Vpkg    string `json:"Vpkg"`
	BaseURL string `json:"BaseURL"`
}

func main() {
	component := slog.String("component", "render")
	slog.Info("render template", component)

	if len(os.Args) != expectedArgCount {
		fmt.Fprintln(os.Stderr, "usage: render <template-path>")
		os.Exit(1)
	}

	raw, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "render: read stdin: %v\n", err)
		os.Exit(1)
	}

	var ctx context
	err = json.Unmarshal(raw, &ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "render: json: %v\n", err)
		os.Exit(1)
	}

	templatePath := filepath.Clean(os.Args[1])
	b, err := os.ReadFile(templatePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "render: read template: %v\n", err)
		os.Exit(1)
	}

	t, err := template.New("t").Delims("[[", "]]").Option("missingkey=error").Parse(string(b))
	if err != nil {
		fmt.Fprintf(os.Stderr, "render: parse: %v\n", err)
		os.Exit(1)
	}

	err = t.Execute(os.Stdout, ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "render: execute: %v\n", err)
		os.Exit(1)
	}
}
