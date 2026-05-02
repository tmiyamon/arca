package main

import (
	"fmt"
	"strconv"
)

func tailTry(s string) (int, error) {
	__try1, __try1_err := strconv.Atoi(s)
	if __try1_err != nil {
		return 0, __try1_err
	}
	return __try1, nil
}

func callArgTry(s string) (int, error) {
	__try1, __try1_err := strconv.Atoi(s)
	if __try1_err != nil {
		return 0, __try1_err
	}
	return __try1 * 2, nil
}

func multipleTry(a string, b string) (int, error) {
	__try1, __try1_err := strconv.Atoi(a)
	if __try1_err != nil {
		return 0, __try1_err
	}
	__try2, __try2_err := strconv.Atoi(b)
	if __try2_err != nil {
		return 0, __try2_err
	}
	return __try1 + __try2, nil
}

func letNestedTry(s string) (int, error) {
	__try1, __try1_err := strconv.Atoi(s)
	if __try1_err != nil {
		return 0, __try1_err
	}
	x := __try1 + 1
	return x, nil
}

func main() {
	fmt.Println(tailTry("7"))
	fmt.Println(callArgTry("8"))
	fmt.Println(multipleTry("3", "4"))
	fmt.Println(letNestedTry("9"))
}
