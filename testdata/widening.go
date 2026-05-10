//go:build amd64 || arm64 || ppc64 || ppc64le || mips64 || mips64le || riscv64 || s390x || loong64 || wasm

package main

type Counter struct {
	Id   int64
	Body string
}

func fromInt8(a int8) int {
	return int(a)
}

func fromInt16(a int16) int64 {
	return int64(a)
}

func fromUInt8(a uint8) uint {
	return uint(a)
}

func fromUInt32(a uint32) uint64 {
	return uint64(a)
}

func makeCounter(id int32, body string) Counter {
	return Counter{int64(id), body}
}
