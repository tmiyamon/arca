package main

type User struct {
	Name string
}

func process(r *User) string {
	return r.Name
}

func main() {
	u := User{"Alice"}
	_ = process(&u)
}
