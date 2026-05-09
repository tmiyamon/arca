package main

import (
	"fmt"
	"os"
	"runtime"
)

// goBuild64BitTag is the build constraint expression that gates Arca-emitted
// Go code to 64-bit GOARCH targets. Arca's `Int = Go int` requires a 64-bit
// platform; on 32-bit GOARCH the file is excluded and `go build` reports
// "no Go files" rather than producing a binary that violates Layer 1.
const goBuild64BitTag = "amd64 || arm64 || ppc64 || ppc64le || mips64 || mips64le || riscv64 || s390x || loong64 || wasm"

// goarch64BitSet mirrors goBuild64BitTag for runtime checks. Keep in sync.
var goarch64BitSet = map[string]struct{}{
	"amd64":    {},
	"arm64":    {},
	"ppc64":    {},
	"ppc64le":  {},
	"mips64":   {},
	"mips64le": {},
	"riscv64":  {},
	"s390x":    {},
	"loong64":  {},
	"wasm":     {},
}

// check64BitTarget verifies the active GOARCH target is 64-bit before invoking
// the Go toolchain. Returns nil if the target is supported, otherwise an error
// suitable for printing to stderr. Honors the GOARCH env var override; falls
// back to runtime.GOARCH (the host arch the arca CLI was built for).
func check64BitTarget() error {
	arch := os.Getenv("GOARCH")
	if arch == "" {
		arch = runtime.GOARCH
	}
	if _, ok := goarch64BitSet[arch]; ok {
		return nil
	}
	return fmt.Errorf("arca requires a 64-bit target: GOARCH=%s is not supported (Int is fixed to 64 bits)", arch)
}
