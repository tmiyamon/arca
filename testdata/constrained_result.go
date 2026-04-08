package main

import (
	"fmt"
)

type Email string

func NewEmail(v string) (Email, error) {
	if len(v) < 5 {
		return "", fmt.Errorf("min length 5")
	}
	if len(v) > 255 {
		return "", fmt.Errorf("max length 255")
	}
	return Email(v), nil
}

func (v Email) ArcaValidate() error {
	_, err := NewEmail(string(v))
	return err
}

func main() {
	__cval1, __cerr1 := NewEmail("test@example.com")
	var result Result_[Email, error]
	if __cerr1 != nil {
		result = Err_[Email, error](__cerr1)
	} else {
		result = Ok_[Email, error](__cval1)
	}
	if result.IsOk {
		email := result.Value
		fmt.Println(email)
	} else {
		err := result.Err
		fmt.Println(err)
	}
}

type Result_[T any, E any] struct {
	Value T
	Err   E
	IsOk  bool
}

func Ok_[T any, E any](v T) Result_[T, E] {
	return Result_[T, E]{Value: v, IsOk: true}
}

func Err_[T any, E any](e E) Result_[T, E] {
	return Result_[T, E]{Err: e}
}
