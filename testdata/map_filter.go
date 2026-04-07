package main

import (
	"fmt"
)

func nums() []int {
	return []int{1, 2, 3, 4, 5}
}

func main() {
	doubled := Map_(nums(), func(x int) int { return x * 2 })
	positives := Filter_(nums(), func(x int) bool { return x > 0 })
	sum := Fold_(nums(), 0, func(acc int, x int) int { return acc + x })
	fmt.Println(doubled)
	fmt.Println(positives)
	fmt.Println(sum)
}

func Map_[T any, U any](list []T, f func(T) U) []U {
	result := make([]U, len(list))
	for i, v := range list {
		result[i] = f(v)
	}
	return result
}

func Filter_[T any](list []T, f func(T) bool) []T {
	var result []T
	for _, v := range list {
		if f(v) {
			result = append(result, v)
		}
	}
	return result
}

func Fold_[T any, U any](list []T, init U, f func(U, T) U) U {
	acc := init
	for _, v := range list {
		acc = f(acc, v)
	}
	return acc
}

