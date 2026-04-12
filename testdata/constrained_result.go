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
	result, result_err := NewEmail("test@example.com")
	if result_err == nil {
		email := result
		fmt.Println(email)
	} else {
		err := result_err
		fmt.Println(err)
	}
}
