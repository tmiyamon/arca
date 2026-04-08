package main

import (
	"fmt"
)

func main() {
	double := func(x int) int { return x * 2 }
	nums := []int{10, 20, 30}
	doubled := Map_(nums, double)
	fmt.Println(doubled)
	fmt.Println(Map_([]int{1, 2, 3}, func(x int) int { return x + 1 }))
}

func Map_[T any, U any](list []T, f func(T) U) []U {
	result := make([]U, len(list))
	for i, v := range list {
		result[i] = f(v)
	}
	return result
}
