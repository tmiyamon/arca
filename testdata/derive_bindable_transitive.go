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

type TodoDraft struct {
	Id   stdlib.BindableSlot[int]
	Body stdlib.BindableSlot[string]
}

func inner[U any, __draftU any](__bindableU stdlib.BindableDict[U, __draftU]) (U, error) {
	d := __bindableU.Draft()
	return __bindableU.Freeze(d)
}

func outer[T any, __draftT any](__bindableT stdlib.BindableDict[T, __draftT]) (T, error) {
	return inner[T, __draftT](__bindableT)
}

func main() {
	if err := func() error {
		_, __err1 := outer[Todo, TodoDraft](__TodoBindable)
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
	return Todo{Id: d.Id.Value, Body: d.Body.Value}, nil
}

func todoDraft() TodoDraft {
	return TodoDraft{}
}

var __TodoBindable = stdlib.BindableDict[Todo, TodoDraft]{Draft: todoDraft, Freeze: TodoDraft.Freeze}
