package main

import (
	"fmt"
)

type Color int

const (
	ColorRed Color = iota
	ColorGreen
	ColorBlue
)

func (v Color) String() string {
	switch v {
	case ColorRed:
		return "Red"
	case ColorGreen:
		return "Green"
	case ColorBlue:
		return "Blue"
	default:
		return "UnknownColor"
	}
}

func colorName(c Color) string {
	switch c {
	case ColorRed:
		return "red"
	case ColorGreen:
		return "green"
	case ColorBlue:
		return "blue"
	default:
		panic("unreachable")
	}
}

func main() {
	fmt.Println("Hello from Arca!")
	fmt.Println(colorName(ColorRed))
	fmt.Println(colorName(ColorBlue))
}

