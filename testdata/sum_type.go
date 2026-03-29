package main

type ApiResponse interface {
	isApiResponse()
}

type ApiResponseSuccess struct {
	Data string
}
func (ApiResponseSuccess) isApiResponse() {}

type ApiResponseErrorResponse struct {
	Message string
	Code int64
}
func (ApiResponseErrorResponse) isApiResponse() {}


