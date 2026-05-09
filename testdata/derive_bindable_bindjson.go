//go:build amd64 || arm64 || ppc64 || ppc64le || mips64 || mips64le || riscv64 || s390x || loong64 || wasm

package main

import (
	"errors"
	"github.com/tmiyamon/arca/stdlib"
	"net/http"
)

type Todo struct {
	Id   int
	Body string
}

type TodoDraft struct {
	Id   stdlib.BindableSlot[int]
	Body stdlib.BindableSlot[string]
}

func Handle(r *http.Request) (Todo, error) {
	__val1, __err1 := stdlib.BindJSON[Todo, TodoDraft](__TodoBindable, r)
	if __err1 != nil {
		return Todo{}, __err1
	}
	t := __val1
	return t, nil
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

func __ptrOf[T any](v T) *T {
	return &v
}

func __optFrom[T any](v T, ok bool) *T {
	if ok {
		return &v
	}
	return nil
}
