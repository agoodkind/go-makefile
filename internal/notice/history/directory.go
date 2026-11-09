package history

import (
	"log/slog"
	"os"
)

type directoryRestoration struct {
	path      string
	attempted bool
}

func (restoration *directoryRestoration) restore() error {
	if restoration.attempted {
		return nil
	}

	restoration.attempted = true
	if err := os.Chdir(restoration.path); err != nil {
		return failed("restore notice working directory", err)
	}

	return nil
}

func (restoration *directoryRestoration) onExit() {
	if err := restoration.restore(); err != nil {
		slog.Error("notice restore directory on exit", slog.Any("error", err))
	}
}
