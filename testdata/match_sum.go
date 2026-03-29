package main

type Result interface {
	isResult()
}

type ResultOk struct {
	Value string
}
func (ResultOk) isResult() {}

type ResultErr struct {
	Message string
}
func (ResultErr) isResult() {}


func unwrap(r Result) string {
	switch v := r.(type) {
	case ResultOk:
		value := v.Value
		return value
	case ResultErr:
		message := v.Message
		return message
	}
	panic("unreachable")
}

