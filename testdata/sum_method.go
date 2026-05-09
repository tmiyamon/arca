//go:build amd64 || arm64 || ppc64 || ppc64le || mips64 || mips64le || riscv64 || s390x || loong64 || wasm

package main

import (
	"fmt"
)

type Animal interface {
	isAnimal()
	speak() string
}

type AnimalDog struct {
	Name string
}

func (AnimalDog) isAnimal() {}

type AnimalCat struct {
	Name string
}

func (AnimalCat) isAnimal() {}

func (a AnimalDog) speak() string {
	name := a.Name
	return fmt.Sprintf("%v says woof", name)
}

func (a AnimalCat) speak() string {
	name := a.Name
	return fmt.Sprintf("%v says meow", name)
}

func main() {
	dog := AnimalDog{Name: "Rex"}
	cat := AnimalCat{Name: "Luna"}
	fmt.Println(dog.speak())
	fmt.Println(cat.speak())
}
