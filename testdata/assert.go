package main

func add(a int, b int) int {
	return a + b
}

func main() {
	if !(add(1, 2) == 3) {
		panic("assertion failed: add(1, 2) == 3")
	}
	if !(add(0, 0) == 0) {
		panic("assertion failed: add(0, 0) == 0")
	}
	if !(1 + 1 == 2) {
		panic("assertion failed: 1 + 1 == 2")
	}
}

