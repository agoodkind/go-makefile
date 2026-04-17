package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"text/template"
)

type context struct {
	Binary  string `json:"Binary"`
	Cmd     string `json:"Cmd"`
	Layout  string `json:"Layout"`
	BaseURL string `json:"BaseURL"`
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: render <template-path>")
		os.Exit(1)
	}
	raw, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "render: read stdin: %v\n", err)
		os.Exit(1)
	}
	var ctx context
	if err := json.Unmarshal(raw, &ctx); err != nil {
		fmt.Fprintf(os.Stderr, "render: json: %v\n", err)
		os.Exit(1)
	}
	b, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "render: read template: %v\n", err)
		os.Exit(1)
	}
	t, err := template.New("t").Delims("[[", "]]").Option("missingkey=error").Parse(string(b))
	if err != nil {
		fmt.Fprintf(os.Stderr, "render: parse: %v\n", err)
		os.Exit(1)
	}
	if err := t.Execute(os.Stdout, ctx); err != nil {
		fmt.Fprintf(os.Stderr, "render: execute: %v\n", err)
		os.Exit(1)
	}
}
