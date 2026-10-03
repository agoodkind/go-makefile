package embedded_test

import (
	_ "embed"
	"testing"
)

//go:embed tmpl.txt
var outsideTemplate string // want `embeds the file pattern "tmpl.txt"`

//go:embed testdata/x.txt
var insideInput string

//go:embed testdata/x.txt tmpl.txt
var mixedPatterns string // want `embeds the file pattern "tmpl.txt"`

func TestEmbedded(t *testing.T) {
	t.Parallel()

	_ = outsideTemplate
	_ = insideInput
	_ = mixedPatterns
}
