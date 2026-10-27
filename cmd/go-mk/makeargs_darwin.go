package main

import (
	"bytes"
	"encoding/binary"
	"fmt"

	"golang.org/x/sys/unix"
)

func readMakeProcess(identifier int) (makeProcessInvocation, error) {
	raw, err := unix.SysctlRaw("kern.procargs2", identifier)
	if err != nil {
		return makeProcessInvocation{}, err
	}
	if len(raw) < 4 {
		return makeProcessInvocation{}, fmt.Errorf("the Make process argument header is incomplete")
	}
	count := int(binary.NativeEndian.Uint32(raw[:4]))
	rest := raw[4:]
	end := bytes.IndexByte(rest, 0)
	if end < 0 || count <= 0 {
		return makeProcessInvocation{}, fmt.Errorf("the Make process argument header is invalid")
	}
	executable := string(rest[:end])
	rest = bytes.TrimLeft(rest[end+1:], "\x00")
	arguments := make([]string, 0, count)
	for range count {
		end = bytes.IndexByte(rest, 0)
		if end < 0 {
			return makeProcessInvocation{}, fmt.Errorf("the Make process argument list is incomplete")
		}
		arguments = append(arguments, string(rest[:end]))
		rest = rest[end+1:]
	}
	environment := make([]string, 0)
	for len(rest) > 0 && rest[0] != 0 {
		end = bytes.IndexByte(rest, 0)
		if end < 0 {
			return makeProcessInvocation{}, fmt.Errorf("the Make process environment is incomplete")
		}
		environment = append(environment, string(rest[:end]))
		rest = rest[end+1:]
	}
	return makeProcessInvocation{executable: executable, arguments: arguments, environment: environment}, nil
}
