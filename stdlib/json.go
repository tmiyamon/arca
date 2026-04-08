package stdlib

import (
	"encoding/json"
)

// Decode parses JSON data into T.
// If T implements Validatable, the result is validated after decoding.
func Decode[T any](data []byte) (T, error) {
	var v T
	if err := json.Unmarshal(data, &v); err != nil {
		return v, err
	}
	if err := ArcaValidateIfPossible(&v); err != nil {
		return v, err
	}
	return v, nil
}

// Encode serializes a value to JSON bytes.
func Encode(v any) ([]byte, error) {
	return json.Marshal(v)
}
