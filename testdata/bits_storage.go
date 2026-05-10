//go:build amd64 || arm64 || ppc64 || ppc64le || mips64 || mips64le || riscv64 || s390x || loong64 || wasm

package main

import (
	"fmt"
)

type Percent uint8

func NewPercent(v uint8) (Percent, error) {
	if v > 100 {
		return 0, fmt.Errorf("must be <= 100")
	}
	return Percent(v), nil
}

type Sample struct {
	Small int16
	Ratio float32
}
