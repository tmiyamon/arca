package stdlib

import (
	"encoding/json"
	"io"
	"net/http"
)

// BindJSON reads JSON from an HTTP request body and decodes it into T.
// If T implements Validatable, the result is validated after decoding.
func BindJSON[T any](r *http.Request) (T, error) {
	var v T
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return v, err
	}
	defer r.Body.Close()
	if err := json.Unmarshal(body, &v); err != nil {
		return v, err
	}
	if err := ArcaValidateIfPossible(&v); err != nil {
		return v, err
	}
	return v, nil
}
