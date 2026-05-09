//go:build amd64 || arm64 || ppc64 || ppc64le || mips64 || mips64le || riscv64 || s390x || loong64 || wasm

package main

import (
	"fmt"
)

type User struct {
	Name string
	Age  int
}

func (u User) isAdult() bool {
	return u.Age >= 18
}

func (u User) ToJson() string {
	return fmt.Sprintf("{\"name\": \"%v\", \"age\": %v}", u.Name, u.Age)
}

func (u User) greet(greeting string) string {
	return fmt.Sprintf("%v, %v!", greeting, u.Name)
}

func main() {
	user := User{Name: "Alice", Age: 30}
	fmt.Println(user.isAdult())
	fmt.Println(user.ToJson())
	fmt.Println(user.greet("Hello"))
}
