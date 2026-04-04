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

type Shape interface {
	isShape()
}

type ShapeCircle struct {
	Radius float64
}
func (ShapeCircle) isShape() {}

type ShapeSquare struct {
	Side float64
}
func (ShapeSquare) isShape() {}


func main() {
	c := ColorRed
	s := ShapeCircle{Radius: 3.14}
	fmt.Println(c)
	fmt.Println(s)
}

