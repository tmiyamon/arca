package main

import (
	"fmt"
)

func apply(f func(int) int, x int) int {
	return f(x)
}

func main() {
	r1 := apply(func(n int) int {
		return n + 1
	}, 41)
	var double func(int) int = func(n int) int {
		return n * 2
	}
	fmt.Println(r1)
	fmt.Println(double(21))
}
