package readfile

import "os"

// Parse counts the bytes of a document.
func Parse(document []byte) int { return len(document) }

// Load reads the document at path and counts its bytes.
func Load(path string) (int, error) {
	document, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return Parse(document), nil
}
