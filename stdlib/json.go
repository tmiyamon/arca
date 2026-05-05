package stdlib

import (
	"encoding/json"
)

// Decode parses JSON data into T via the Bindable dictionary: it builds an
// empty Draft, lets `json.Unmarshal` populate the BindableSlot fields
// (their UnmarshalJSON sets `Set: true`), and then `Freeze`-s the Draft
// through the user's NewT-equivalent constructor for constraint validation.
// `dict` is injected by the Arca compiler — `stdlib.Decode[Todo](data)`
// rewrites to `stdlib.Decode[Todo, TodoDraft](__TodoBindable, data)`.
func Decode[T any, B any](dict BindableDict[T, B], data []byte) (T, error) {
	d := dict.Draft()
	if err := json.Unmarshal(data, &d); err != nil {
		var zero T
		return zero, err
	}
	return dict.Freeze(d)
}

// Encode serializes a value to JSON bytes.
func Encode(v any) ([]byte, error) {
	return json.Marshal(v)
}
