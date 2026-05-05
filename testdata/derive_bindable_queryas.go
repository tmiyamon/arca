package main

import (
	"database/sql"
	"errors"
	"github.com/tmiyamon/arca/stdlib"
)

type Todo struct {
	Id   int    `db:"id"`
	Body string `db:"body"`
}

type TodoDraft struct {
	Id   stdlib.BindableSlot[int]    `db:"id"`
	Body stdlib.BindableSlot[string] `db:"body"`
}

func ListTodos(db *sql.DB) ([]Todo, error) {
	return stdlib.QueryAs[Todo, TodoDraft](__TodoBindable, db, "SELECT id, body FROM todos")
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
