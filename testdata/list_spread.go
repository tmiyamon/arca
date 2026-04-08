package main

import (
	"fmt"
)

func main() {
	a := []int{1, 2, 3}
	b := append([]int{0}, a...)
	fmt.Println(b)
}
