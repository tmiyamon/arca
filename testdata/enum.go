//go:build amd64 || arm64 || ppc64 || ppc64le || mips64 || mips64le || riscv64 || s390x || loong64 || wasm

package main

type Color int

const (
	ColorRed Color = iota
	ColorGreen
	ColorBlue
)

func (v Color) String() string {
	switch v {
	case ColorRed:
		return "Red"
	case ColorGreen:
		return "Green"
	case ColorBlue:
		return "Blue"
	default:
		return "UnknownColor"
	}
}
