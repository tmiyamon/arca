//go:build amd64 || arm64 || ppc64 || ppc64le || mips64 || mips64le || riscv64 || s390x || loong64 || wasm

package main

import (
	"fmt"
)

type Greeting interface {
	isGreeting()
}

type GreetingHello struct {
	Name string
}

func (GreetingHello) isGreeting() {}

type GreetingGoodbye struct {
	Name string
}

func (GreetingGoodbye) isGreeting() {}

func greetingCreate(s string) Greeting {
	return GreetingHello{Name: s}
}

func main() {
	g := greetingCreate("World")
	fmt.Println(g)
}
