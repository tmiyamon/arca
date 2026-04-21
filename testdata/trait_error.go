package main

import (
	"fmt"
)

type NotFound struct {
	Id int
}

type ArcaError interface {
	Message() string
}

func (n NotFound) Message() string {
	return fmt.Sprintf("not found: %v", n.Id)
}

func (n NotFound) Error() string {
	return n.Message()
}

func main() {
	e := NotFound{Id: 42}
	fmt.Println(e.Message())
}
