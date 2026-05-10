//go:build amd64 || arm64 || ppc64 || ppc64le || mips64 || mips64le || riscv64 || s390x || loong64 || wasm

package main

import (
	"fmt"
	"math"
	"os"
)

type Sample struct {
	A int8
	B uint32
	C float32
	D int64
}

func main() {
	if err := func() error {
		_, __err1 := NewInt8(int64(100))
		if __err1 != nil {
			return __err1
		}
		_, __err2 := NewUInt32(uint64(20))
		if __err2 != nil {
			return __err2
		}
		_, __err3 := NewFloat32(float64(3.14))
		if __err3 != nil {
			return __err3
		}
		_, r_err := NewInt8(int64(200))
		if r_err == nil {
			fmt.Println("unexpected ok")
		} else {
			fmt.Println("out of range caught")
		}
		return nil
	}(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func NewInt8(v int64) (int8, error) {
	if v < math.MinInt8 || v > math.MaxInt8 {
		return 0, fmt.Errorf("Int8: value %d out of range [%d, %d]", v, int64(math.MinInt8), int64(math.MaxInt8))
	}
	return int8(v), nil
}

func NewUInt32(v uint64) (uint32, error) {
	if v > math.MaxUint32 {
		return 0, fmt.Errorf("UInt32: value %d out of range [0, %d]", v, uint64(math.MaxUint32))
	}
	return uint32(v), nil
}

func NewFloat32(v float64) (float32, error) {
	f := float32(v)
	if math.IsInf(float64(f), 0) && !math.IsInf(v, 0) {
		return 0, fmt.Errorf("Float32: value %g out of range", v)
	}
	return f, nil
}
