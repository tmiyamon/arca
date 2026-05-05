package main

import (
	"errors"
	"fmt"
	"os"
)

type Todo struct {
	Id   int
	Body string
}

type BindableSlot[T any] struct {
	Set   bool
	Value T
}

type BindableDict[T any, B any] struct {
	Draft  func() B
	Freeze func(B) (T, error)
}

type TodoDraft struct {
	Id   BindableSlot[int]
	Body BindableSlot[string]
}

func makeIt[T any, __draftT any](__bindableT BindableDict[T, __draftT]) (T, error) {
	d := __bindableT.Draft()
	return __bindableT.Freeze(d)
}

func main() {
	if err := func() error {
		_, __err1 := makeIt[Todo, TodoDraft](__TodoBindable)
		if __err1 != nil {
			return __err1
		}
		return nil
	}(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func __TodoFreeze(d TodoDraft) (Todo, error) {
	if d.Id.Set == false {
		return Todo{}, errors.New("Todo.id is unset")
	}
	if d.Body.Set == false {
		return Todo{}, errors.New("Todo.body is unset")
	}
	return Todo{Id: d.Id.Value, Body: d.Body.Value}, nil
}

var __TodoBindable = BindableDict[Todo, TodoDraft]{Draft: func() TodoDraft {
	return TodoDraft{}
}, Freeze: __TodoFreeze}
