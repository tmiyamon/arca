package main

import (
	"fmt"
)

func classify(n int) string {
	if n > 0 {
		return "positive"
	} else {
		if n < 0 {
			return "negative"
		} else {
			return "zero"
		}
	}
}

func describe(x int) string {
	if x == 0 {
		return "zero"
	} else {
		return "nonzero"
	}
}

func main() {
	fmt.Println(classify(42))
	fmt.Println(classify(-1))
	fmt.Println(classify(0))
	fmt.Println(describe(5))
	var label string
	if 1 > 0 {
		label = "yes"
	} else {
		label = "no"
	}
	fmt.Println(label)
}
