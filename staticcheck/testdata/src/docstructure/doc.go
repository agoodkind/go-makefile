package docstructure

// Short returns one. // want `doc_structure: add an introduction`
func Short() int { return 1 }

// Heading returns one. // want `doc_structure: add an introduction`
//
// The result is one.
//
// # Examples
func Heading() int { return 1 }

// Complete returns one.
//
// The result is always one.
//
// Example:
//
//	Complete() == 1
func Complete() int { return 1 }

// List returns one.
//
// The result is always one.
//
// Inputs:
//   - None.
func List() int { return 1 }

// private returns one.
func private() int { return 1 }

func Missing() int { return 1 }
