package main

import (
	"fmt"
)

func main() {
	ages := map[string]int{"alice": 30, "bob": 25}
	var empty map[string]int = map[string]int{}
	n := ages["alice"]
	fmt.Println(ages)
	fmt.Println(empty)
	fmt.Println(n)
}
