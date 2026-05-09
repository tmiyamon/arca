//go:build amd64 || arm64 || ppc64 || ppc64le || mips64 || mips64le || riscv64 || s390x || loong64 || wasm

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
	return Todo{Id: d.Id.Value, Body: d.Body.Value}, nil
}

func todoDraft() TodoDraft {
	return TodoDraft{}
}

var __TodoBindable = stdlib.BindableDict[Todo, TodoDraft]{Draft: todoDraft, Freeze: TodoDraft.Freeze}
