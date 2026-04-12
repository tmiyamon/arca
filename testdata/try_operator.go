package main

import (
	"fmt"
	"strconv"
)

func parseAndDouble(s string) (int, error) {
	__val1, __err1 := strconv.Atoi(s)
	if __err1 != nil {
		return 0, __err1
	}
	n := __val1
	return n * 2, nil
}

func main() {
	result, result_err := parseAndDouble("21")
	if result_err == nil {
		n := result
		fmt.Println(n)
	} else {
		err := result_err
		fmt.Println(err)
	}
}
