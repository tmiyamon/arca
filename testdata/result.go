package main

import (
	"fmt"
	"strconv"
)

func parseAndDouble(s string) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, err
	}
	return n + n, nil
}

func main() {
	fmt.Println(parseAndDouble("21"))
	fmt.Println(parseAndDouble("abc"))
}

