package main

import (
	"fmt"
)

func main() {
	sql := `SELECT *
FROM users
WHERE id = 1`
	fmt.Println(sql)
	name := "Alice"
	msg := fmt.Sprintf(`Hello %v!
Welcome to Arca.
`, name)
	fmt.Println(msg)
}
