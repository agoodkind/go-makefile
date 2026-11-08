//go:build formatting

package widget

func Tagged(value int) int {
	if value < 0 {
		return 0
	}
	return value
}
