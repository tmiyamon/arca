package main

import (
	"fmt"
)

func test() (int, error) {
	return 42, nil
}

func main() {
	_, result_err := test()
	if result_err != nil {
		e := result_err
		fmt.Println(e)
	}
}
