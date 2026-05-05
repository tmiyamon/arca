package stdlib

import "encoding/json"

// BindableSlot is the per-field slot of a `derive Bindable` Draft. It tracks
// whether a value has been provided (Set) and stores it (Value). Generated
// `<T>Draft` structs use it for every field; the `freeze()` synthesised
// method rejects any Draft with an unset slot.
//
// UnmarshalJSON marks Set=true when the field is present in the input,
// matching Go json.Unmarshal's "absent → leave zero" semantics. This lets
// stdlib's JSON helpers populate a Draft directly with `json.Unmarshal`.
type BindableSlot[T any] struct {
	Set   bool
	Value T
}

func (s *BindableSlot[T]) UnmarshalJSON(data []byte) error {
	if err := json.Unmarshal(data, &s.Value); err != nil {
		return err
	}
	s.Set = true
	return nil
}

// BindableDict is the dispatch dictionary the compiler synthesises for each
// `derive Bindable` host. Generic functions taking `[T: Bindable]` receive
// it as a hidden first parameter; user code never references it directly.
type BindableDict[T any, B any] struct {
	Draft  func() B
	Freeze func(B) (T, error)
}
