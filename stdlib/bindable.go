package stdlib

import (
	"encoding/json"
	"fmt"
	"reflect"
)

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

// Scan implements sql.Scanner so a `*BindableSlot[T]` can be passed as a
// destination to `rows.Scan`. SQL drivers report values as int64 / float64 /
// string / []byte / time.Time / bool / nil — we accept a direct match, fall
// back to reflect.Value.Convert for the common driver-widening cases (e.g.
// int64 → int), and leave Set=false on a nil column.
func (s *BindableSlot[T]) Scan(value any) error {
	if value == nil {
		return nil
	}
	rt := reflect.TypeOf((*T)(nil)).Elem()
	rv := reflect.ValueOf(value)
	if rv.Type().AssignableTo(rt) {
		s.Value = rv.Interface().(T)
		s.Set = true
		return nil
	}
	if rv.Type().ConvertibleTo(rt) {
		s.Value = rv.Convert(rt).Interface().(T)
		s.Set = true
		return nil
	}
	return fmt.Errorf("BindableSlot[%v].Scan: cannot accept %T", rt, value)
}

// BindableDict is the dispatch dictionary the compiler synthesises for each
// `derive Bindable` host. Generic functions taking `[T: Bindable]` receive
// it as a hidden first parameter; user code never references it directly.
type BindableDict[T any, B any] struct {
	Draft  func() B
	Freeze func(B) (T, error)
}
