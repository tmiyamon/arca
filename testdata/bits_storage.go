//go:build amd64 || arm64 || ppc64 || ppc64le || mips64 || mips64le || riscv64 || s390x || loong64 || wasm

package main

type Int8 int8

type Int16 int16

type Int32 int32

type Int64 int64

type UInt8 uint8

type UInt16 uint16

type UInt32 uint32

type UInt64 uint64

type Float32 float32

type Float64 float64

type Counter struct {
	Small int16
	Big   UInt32
}
