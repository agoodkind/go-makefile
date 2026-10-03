module example.com/testdouble

go 1.26

require (
	github.com/golang/mock v0.0.0
	github.com/maxbrunsfeld/counterfeiter v0.0.0
	github.com/stretchr/testify v0.0.0
	github.com/vektra/mockery v0.0.0
	go.uber.org/mock v0.0.0
)

replace github.com/golang/mock => ./stubs/golangmock

replace github.com/maxbrunsfeld/counterfeiter => ./stubs/counterfeiter

replace github.com/stretchr/testify => ./stubs/testify

replace github.com/vektra/mockery => ./stubs/mockery

replace go.uber.org/mock => ./stubs/ubermock
