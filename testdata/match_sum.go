package main

type Response interface {
	isResponse()
}

type ResponseOk struct {
	Value string
}
func (ResponseOk) isResponse() {}

type ResponseErr struct {
	Message string
}
func (ResponseErr) isResponse() {}


func unwrap(r Response) string {
	switch v := r.(type) {
	case ResponseOk:
		value := v.Value
		return value
	case ResponseErr:
		message := v.Message
		return message
	}
	panic("unreachable")
}

