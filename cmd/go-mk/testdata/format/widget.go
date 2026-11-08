package widget

func sum(first, second int) int { return first + second }

func Double(value int) int {
	if value < 0 {
		return 0
	}
	return sum(
		value, value,
	)
}
