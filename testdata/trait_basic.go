package main

import (
	"fmt"
)

type ArcaDisplay interface {
	Show() string
}

type User struct {
	Name string
}

func (u User) Show() string {
	return u.Name
}

func render(d ArcaDisplay) string {
	return d.Show()
}

func main() {
	u := User{Name: "Alice"}
	fmt.Println(render(u))
}
