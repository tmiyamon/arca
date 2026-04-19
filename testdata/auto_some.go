package main

import (
	"fmt"
)

func describe(n *int) string {
	if n != nil {
		v := *n
		return fmt.Sprintf("value is %v", v)
	} else {
		return "nothing"
	}
}

func wrap(x int) *int {
	return __ptrOf(x)
}

func main() {
	var a *int = __ptrOf(10)
	var b *int = (*int)(nil)
	fmt.Println(describe(a))
	fmt.Println(describe(b))
	fmt.Println(describe(__ptrOf(42)))
	fmt.Println(describe(wrap(7)))
}

func __ptrOf[T any](v T) *T {
	return &v
}

func __optFrom[T any](v T, ok bool) *T {
	if ok {
		return &v
	}
	return nil
}
