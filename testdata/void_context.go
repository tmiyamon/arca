package main

import (
	"fmt"
)

func test() (int, error) {
	return 42, nil
}

func main() {
	_, result_err := test()
	if result_err != nil {
		e := __goError{inner: result_err}
		fmt.Println(e)
	}
}

type __goError struct{ inner error }

func (e __goError) Message() string {
	return e.inner.Error()
}

func (e __goError) Error() string {
	return e.inner.Error()
}

func (e __goError) Unwrap() error {
	return e.inner
}
