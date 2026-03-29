package main

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

