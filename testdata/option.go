package main

import (
	"fmt"
)

func greet(name string, name_ok bool) string {
	if name_ok {
		n := name
		return fmt.Sprintf("Hello %v!", n)
	} else {
		return "Hello stranger!"
	}
}

func main() {
	fmt.Println(greet("Alice", true))
	fmt.Println(greet("", false))
}
