package main

import (
	"fmt"
)

func pair() struct {
	First  int
	Second string
} {
	return struct {
		First  int
		Second string
	}{42, "hello"}
}

func main() {
	__tuple1 := pair()
	num := __tuple1.First
	name := __tuple1.Second
	fmt.Println(num)
	fmt.Println(name)
}
