//go:build amd64 || arm64 || ppc64 || ppc64le || mips64 || mips64le || riscv64 || s390x || loong64 || wasm

package main

type ApiResponse interface {
	isApiResponse()
}

type ApiResponseSuccess struct {
	Data string
}

func (ApiResponseSuccess) isApiResponse() {}

type ApiResponseErrorResponse struct {
	Message string
	Code    int
}

func (ApiResponseErrorResponse) isApiResponse() {}
