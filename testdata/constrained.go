package main

import (
	"fmt"
	"regexp"
)

type PositiveInt int

func NewPositiveInt(v int) (PositiveInt, error) {
	if v < 1 {
		return 0, fmt.Errorf("must be >= 1")
	}
	return PositiveInt(v), nil
}

func (v PositiveInt) Validate() error {
	_, err := NewPositiveInt(int(v))
	return err
}

type Email string

func NewEmail(v string) (Email, error) {
	if !regexp.MustCompile(".+@.+").MatchString(string(v)) {
		return "", fmt.Errorf("must match pattern")
	}
	if len(v) > 255 {
		return "", fmt.Errorf("max length 255")
	}
	return Email(v), nil
}

func (v Email) Validate() error {
	_, err := NewEmail(string(v))
	return err
}

type User struct {
	Id int
	Name string
	Age int
}

func NewUser(id int, name string, age int) (User, error) {
	if id < 1 {
		return User{}, fmt.Errorf("id: must be >= 1")
	}
	if len(name) < 1 {
		return User{}, fmt.Errorf("name: min length 1")
	}
	if len(name) > 100 {
		return User{}, fmt.Errorf("name: max length 100")
	}
	if age < 0 {
		return User{}, fmt.Errorf("age: must be >= 0")
	}
	if age > 150 {
		return User{}, fmt.Errorf("age: must be <= 150")
	}
	return User{Id: id, Name: name, Age: age}, nil
}

func (v User) Validate() error {
	_, err := NewUser(v.Id, v.Name, v.Age)
	return err
}

func createUser() Result_[User, error] {
	__try_val1, __try_err1 := NewUser(1, "Alice", 30)
	if __try_err1 != nil {
		return Err_[User, error](__try_err1)
	}
	user := __try_val1
	return Ok_[User, error](user)
}

func main() {
	result := createUser()
	fmt.Println(result)
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

