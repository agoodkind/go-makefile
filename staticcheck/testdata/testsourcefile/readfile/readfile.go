package readfile

import "os"

func Parse(document []byte) int { return len(document) }

func Load(path string) (int, error) {
	document, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return Parse(document), nil
}
