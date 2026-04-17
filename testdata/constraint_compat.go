package main

import (
	"fmt"
)

type Age int

func NewAge(v int) (Age, error) {
	if v < 0 {
		return 0, fmt.Errorf("must be >= 0")
	}
	if v > 150 {
		return 0, fmt.Errorf("must be <= 150")
	}
	return Age(v), nil
}

func (v Age) ArcaValidate() error {
	_, err := NewAge(int(v))
	return err
}

type AdultAge int

func NewAdultAge(v int) (AdultAge, error) {
	if v < 18 {
		return 0, fmt.Errorf("must be >= 18")
	}
	if v > 150 {
		return 0, fmt.Errorf("must be <= 150")
	}
	return AdultAge(v), nil
}

func (v AdultAge) ArcaValidate() error {
	_, err := NewAdultAge(int(v))
	return err
}

func greet(age Age) string {
	return "hello"
}

func main() {
	r, r_err := func() (string, error) {
		__val1, __err1 := NewAdultAge(25)
		if __err1 != nil {
			return "", __err1
		}
		adult := __val1
		return greet(Age(adult)), nil
	}()
	if r_err == nil {
		msg := r
		fmt.Println(msg)
	} else {
		err := r_err
		fmt.Println(err)
	}
}
