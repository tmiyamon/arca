package main

import (
	"fmt"
)

func greet(name Option_[string]) string {
	if name.Valid {
		n := name.Value
		return fmt.Sprintf("Hello %v!", n)
	} else {
		return "Hello stranger!"
	}
}

func main() {
	fmt.Println(greet(Some_("Alice")))
	fmt.Println(greet(None_[string]()))
}

type Option_[T any] struct {
	Value T
	Valid bool
}

func Some_[T any](v T) Option_[T] {
	return Option_[T]{Value: v, Valid: true}
}

func None_[T any]() Option_[T] {
	return Option_[T]{}
}

