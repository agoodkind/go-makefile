package widget

func sum(left, right int) int {
	return left + right
}

// Double returns twice the supplied value.
//
// It combines additions without changing the input.
//
// For example:
//
//	Double(2) // 4
func Double(value int) int {
	first := sum(value, value)
	second := sum(first, value)
	return sum(second, -value)
}
