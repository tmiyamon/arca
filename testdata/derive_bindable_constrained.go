package main

import (
	"errors"
	"fmt"
	"github.com/tmiyamon/arca/stdlib"
	"os"
)

type Todo struct {
	Id   int
	Body string
}

func NewTodo(id int, body string) (Todo, error) {
	if len(body) > 255 {
		return Todo{}, fmt.Errorf("body: max length 255")
	}
	return Todo{Id: id, Body: body}, nil
}

func (v Todo) ArcaValidate() error {
	_, err := NewTodo(v.Id, v.Body)
	return err
}

type TodoDraft struct {
	Id   stdlib.BindableSlot[int]
	Body stdlib.BindableSlot[string]
}

func main() {
	if err := func() error {
		d := todoDraft()
		_, __err1 := d.Freeze()
		if __err1 != nil {
			return __err1
		}
		return nil
	}(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func (d TodoDraft) Freeze() (Todo, error) {
	if d.Id.Set == false {
		return Todo{}, errors.New("Todo.id is unset")
	}
	if d.Body.Set == false {
		return Todo{}, errors.New("Todo.body is unset")
	}
	return NewTodo(d.Id.Value, d.Body.Value)
}

func todoDraft() TodoDraft {
	return TodoDraft{}
}

var __TodoBindable = stdlib.BindableDict[Todo, TodoDraft]{Draft: todoDraft, Freeze: TodoDraft.Freeze}
