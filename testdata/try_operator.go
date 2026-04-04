package main

import (
	"strconv"
	"fmt"
)

func parseAndDouble(s string) Result_[int, error] {
	__try_val1, __try_err1 := strconv.Atoi(s)
	if __try_err1 != nil {
		return Err_[int, error](__try_err1)
	}
	n := __try_val1
	return Ok_[int, error](n * 2)
}

func main() {
	result := parseAndDouble("21")
	if result.IsOk {
		n := result.Value
		fmt.Println(n)
	} else {
		err := result.Err
		fmt.Println(err)
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

