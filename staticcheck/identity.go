package staticcheck

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/ast"
	"go/format"
	"go/token"
)

const ruleHashBytes = 16

func ruleFingerprint(node ast.Node) (string, error) {
	var source bytes.Buffer
	if err := format.Node(&source, token.NewFileSet(), node); err != nil {
		return "", fmt.Errorf("identify source occurrence: %w", err)
	}

	digest := sha256.Sum256(source.Bytes())

	return hex.EncodeToString(digest[:ruleHashBytes]), nil
}
