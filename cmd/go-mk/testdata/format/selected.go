package widget

import "errors"

func Selected(value int) int {
	if value < 0 {
		return 0
	}
	return value
}

func SelectedError() error {
	err := errors.New("selected")

	if err != nil {
		return err
	}
	return nil
}
