package mocks_test

import (
	_ "errors"
	_ "github.com/golang/mock"                      // want `The import "github\.com/golang/mock" is a mock library\. A test must run the production implementation with real dependencies\. Remove the mock and run the production implementation\.`
	_ "github.com/maxbrunsfeld/counterfeiter"       // want `The import "github\.com/maxbrunsfeld/counterfeiter" is a mock library\.`
	_ "github.com/stretchr/testify/mock"            // want `The import "github\.com/stretchr/testify/mock" is a mock library\.`
	_ "github.com/vektra/mockery"                   // want `The import "github\.com/vektra/mockery" is a mock library\.`
	_ "go.uber.org/mock"                            // want `The import "go\.uber\.org/mock" is a mock library\.`
	_ "go.uber.org/mock/gomock"                     // want `The import "go\.uber\.org/mock/gomock" is a mock library\.`
)
