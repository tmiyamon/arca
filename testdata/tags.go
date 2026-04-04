package main

type User struct {
	Id int `json:"id" db:"id"`
	UserName string `json:"userName" db:"user_name"`
}

