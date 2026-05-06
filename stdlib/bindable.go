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
// destination to `rows.Scan`. SQL drivers report values as int64 / float64
// / string / []byte / time.Time / bool / nil — we mark Set=true once the
// driver delivers any value (including nil), keeping the slot's "Set"
// semantics aligned with "input was provided" rather than "value is
// non-null". For pointer-typed T (Arca Option-backed) nil is a valid
// payload; for non-pointer T it surfaces a Scanner error so the row
// scan halts before freeze.
func (s *BindableSlot[T]) Scan(value any) error {
	rt := reflect.TypeOf((*T)(nil)).Elem()

	if value == nil {
		if rt.Kind() == reflect.Ptr {
			// Option-backed (*X): null is a valid payload representing
			// None. Value is already the zero value (typed nil pointer).
			s.Set = true
			return nil
		}
		return fmt.Errorf("BindableSlot[%v].Scan: cannot accept nil for non-nullable type", rt)
	}

	rv := reflect.ValueOf(value)

	// For pointer-typed T (Option-backed), wrap the incoming non-nil value
	// into a freshly allocated pointer so callers see Some(value).
	if rt.Kind() == reflect.Ptr {
		elemType := rt.Elem()
		var converted reflect.Value
		switch {
		case rv.Type().AssignableTo(elemType):
			converted = rv
		case rv.Type().ConvertibleTo(elemType):
			converted = rv.Convert(elemType)
		default:
			return fmt.Errorf("BindableSlot[%v].Scan: cannot accept %T", rt, value)
		}
		ptr := reflect.New(elemType)
		ptr.Elem().Set(converted)
		s.Value = ptr.Interface().(T)
		s.Set = true
		return nil
	}

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
