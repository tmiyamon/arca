package main

import (
	"fmt"
)

type Pair[A any, B any] struct {
	First A
	Second B
}

func main() {
	p := Pair[int, string]{First: 1, Second: "hello"}
	fmt.Println(p.First)
	fmt.Println(p.Second)
}

