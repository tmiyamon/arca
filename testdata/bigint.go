//go:build amd64 || arm64 || ppc64 || ppc64le || mips64 || mips64le || riscv64 || s390x || loong64 || wasm

package main

import (
	"fmt"
	"github.com/tmiyamon/arca/stdlib"
	"os"
)

func main() {
	if err := func() error {
		a := stdlib.NewBigInt(1000000)
		b := stdlib.NewBigInt(1000000)
		product := a.Mul(b)
		fmt.Println(product.String())
		__val1, __err1 := stdlib.BigIntFromString("123456789012345678901234567890")
		if __err1 != nil {
			return __err1
		}
		huge := __val1
		fmt.Println(huge.String())
		return nil
	}(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
