package main

import (
	"fmt"
)

func test() Result_[int, error] {
	return Ok_[int, error](42)
}

func main() {
	result := test()
	if !result.IsOk {
		e := result.Err
		fmt.Println(e)
	}
}

type Result_[T any, E any] struct {
	Value T
	Err   E
	IsOk  bool
}

func Ok_[T any, E any](v T) Result_[T, E] {
	return Result_[T, E]{Value: v, IsOk: true}
}

func Err_[T any, E any](e E) Result_[T, E] {
	return Result_[T, E]{Err: e}
}
