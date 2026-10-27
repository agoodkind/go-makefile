package main

import (
	"bytes"
	"fmt"
	"os"
)

func readMakeProcess(identifier int) (makeProcessInvocation, error) {
	executable, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", identifier))
	if err != nil {
		return makeProcessInvocation{}, err
	}
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", identifier))
	if err != nil {
		return makeProcessInvocation{}, err
	}
	fields := bytes.Split(bytes.TrimSuffix(raw, []byte{0}), []byte{0})
	arguments := make([]string, 0, len(fields))
	for _, field := range fields {
		arguments = append(arguments, string(field))
	}
	environmentRaw, err := os.ReadFile(fmt.Sprintf("/proc/%d/environ", identifier))
	if err != nil {
		return makeProcessInvocation{}, err
	}
	environment := make([]string, 0)
	if len(environmentRaw) > 0 {
		for _, entry := range bytes.Split(bytes.TrimSuffix(environmentRaw, []byte{0}), []byte{0}) {
			environment = append(environment, string(entry))
		}
	}
	return makeProcessInvocation{executable: executable, arguments: arguments, environment: environment}, nil
}
