module example.com/testdouble

go 1.26

require (
	github.com/stretchr/testify v0.0.0
	go.uber.org/mock v0.0.0
)

replace github.com/stretchr/testify => ./stubs/testify

replace go.uber.org/mock => ./stubs/ubermock
