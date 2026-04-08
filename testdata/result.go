package main

import (
	"fmt"
	"strconv"
)

func parseAndDouble(s string) Result_[int, error] {
	__try_val1, __try_err1 := strconv.Atoi(s)
	if __try_err1 != nil {
		return Err_[int, error](__try_err1)
	}
	n := __try_val1
	return Ok_[int, error](n + n)
}

func main() {
	fmt.Println(parseAndDouble("21"))
	fmt.Println(parseAndDouble("abc"))
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
