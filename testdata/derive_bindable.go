package main

import (
	"fmt"
)

type Todo struct {
	Id   int
	Body string
}

func (t Todo) describe() string {
	return t.Body
}

func main() {
	t := Todo{Id: 1, Body: "draft"}
	fmt.Println(t.describe())
}
