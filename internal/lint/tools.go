package lint

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

//go:embed tools.json
var toolPins []byte

type toolPin struct {
	Override string `json:"override"`
	Install  string `json:"install"`
}

var readToolPins = sync.OnceValues(func() ([]toolPin, error) {
	var pins []toolPin
	if err := json.Unmarshal(toolPins, &pins); err != nil {
		return nil, fmt.Errorf("read lint tools.json: %w", err)
	}

	for _, pin := range pins {
		if pin.Override == "" || pin.Install == "" {
			return nil, fmt.Errorf(
				"each pin in lint tools.json requires an override and install specification",
			)
		}
	}

	return pins, nil
})

func GolangciInstallSpec() (string, error) {
	pins, err := readToolPins()
	if err != nil {
		return "", err
	}

	for _, pin := range pins {
		if pin.Override == "GOLANGCI_LINT_INSTALL" {
			return pin.spec(), nil
		}
	}

	return "", fmt.Errorf("declare GOLANGCI_LINT_INSTALL in lint tools.json")
}

func PrepareTools(install func(string) error, executable func(string) bool) error {
	binary := os.Getenv("GOLANGCI_LINT")
	if filepath.IsAbs(binary) {
		if !executable(binary) {
			return fmt.Errorf("GOLANGCI_LINT is not executable: %s", binary)
		}

		return nil
	}

	pins, err := readToolPins()
	if err != nil {
		return err
	}

	for _, pin := range pins {
		if err := install(pin.spec()); err != nil {
			return err
		}
	}

	return nil
}

func (pin toolPin) spec() string {
	if override := strings.TrimSpace(os.Getenv(pin.Override)); override != "" {
		return override
	}

	return pin.Install
}
