//go:build !darwin && !linux

package main

import "fmt"

func readMakeProcess(_ int) (makeProcessInvocation, error) {
	return makeProcessInvocation{}, fmt.Errorf("recipe validation requires a Linux or macOS Make process")
}
