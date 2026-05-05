package stdlib

import (
	"encoding/json"
	"io"
	"net/http"
)

// BindJSON reads JSON from an HTTP request body and decodes it into T via
// the Bindable dictionary (see Decode for the populate → freeze flow).
// `dict` is injected by the Arca compiler.
func BindJSON[T any, B any](dict BindableDict[T, B], r *http.Request) (T, error) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var zero T
		return zero, err
	}
	defer r.Body.Close()
	d := dict.Draft()
	if err := json.Unmarshal(body, &d); err != nil {
		var zero T
		return zero, err
	}
	return dict.Freeze(d)
}
