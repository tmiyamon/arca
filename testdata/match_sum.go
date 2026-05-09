//go:build amd64 || arm64 || ppc64 || ppc64le || mips64 || mips64le || riscv64 || s390x || loong64 || wasm

package main

type Response interface {
	isResponse()
}

type ResponseOk struct {
	Value string
}

func (ResponseOk) isResponse() {}

type ResponseErr struct {
	Message string
}

func (ResponseErr) isResponse() {}

func unwrap(r Response) string {
	switch v := r.(type) {
	case ResponseOk:
		value := v.Value
		return value
	case ResponseErr:
		message := v.Message
		return message
	}
	panic("unreachable")
}
