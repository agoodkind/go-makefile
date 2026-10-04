package mocks_test

import (
	_ "errors"
	_ "github.com/stretchr/testify/mock" // want `The import "github\.com/stretchr/testify/mock" is a mock library\. A test must run the production implementation with real dependencies\. Remove the mock and run the production implementation\.`
	_ "go.uber.org/mock/gomock"          // want `The import "go\.uber\.org/mock/gomock" is a mock library\.`
)
