package main

import (
	"fmt"
)

type User struct {
	Id   int
	Name string
}

func main() {
	var users []User
	var count int = 42
	fmt.Println(users, count)
}
