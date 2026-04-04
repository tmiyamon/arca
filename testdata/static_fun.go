package main

import (
	"fmt"
)

type Greeting interface {
	isGreeting()
}

type GreetingHello struct {
	Name string
}
func (GreetingHello) isGreeting() {}

type GreetingGoodbye struct {
	Name string
}
func (GreetingGoodbye) isGreeting() {}


func greetingCreate(s string) Greeting {
	return GreetingHello{Name: s}
}

func main() {
	g := greetingCreate("World")
	fmt.Println(g)
}

