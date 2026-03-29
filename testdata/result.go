package main

import (
	"fmt"
	"strconv"
)

func parse_and_double(s string) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, err
	}
	return n + n, nil
}

func main() {
	fmt.Println(parse_and_double("21"))
	fmt.Println(parse_and_double("abc"))
}

