package stdlib

import (
	"database/sql"
	"fmt"
	"reflect"
)

// QueryAs executes a query and returns results as a slice of T via the
// Bindable dictionary: each row populates a fresh Draft (its
// `BindableSlot[X]` fields implement sql.Scanner so rows.Scan writes
// directly into them and marks Set=true), then `Freeze` validates and
// constructs T. The compiler injects `dict` from `__<Type>Bindable`.
//
// Column-to-field mapping uses the Draft's `db:"col"` struct tags
// (mirrored from the host's `tags { db(...) }` block by B2b-tag),
// falling back to the field name for unmapped columns.
func QueryAs[T any, B any](dict BindableDict[T, B], db *sql.DB, query string, args ...any) ([]T, error) {
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	results := make([]T, 0)
	for rows.Next() {
		d := dict.Draft()
		ptrs, err := draftScanPtrs(&d, columns)
		if err != nil {
			return nil, err
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		v, err := dict.Freeze(d)
		if err != nil {
			return nil, fmt.Errorf("freeze failed for row: %w", err)
		}
		results = append(results, v)
	}
	return results, rows.Err()
}

// QueryOneAs executes a query and returns a single result.
// Returns an error if no rows are found.
func QueryOneAs[T any, B any](dict BindableDict[T, B], db *sql.DB, query string, args ...any) (T, error) {
	results, err := QueryAs(dict, db, query, args...)
	if err != nil {
		var zero T
		return zero, err
	}
	if len(results) == 0 {
		var zero T
		return zero, fmt.Errorf("no rows found")
	}
	return results[0], nil
}

// draftScanPtrs returns a slice of pointers into a Draft struct's fields,
// each one a `*BindableSlot[X]` so rows.Scan invokes BindableSlot.Scan and
// records Set=true on populate. Mapping is `db:` struct tag → field, with
// field-name fallback; unmapped columns scan into a discard.
func draftScanPtrs(v any, columns []string) ([]any, error) {
	rv := reflect.ValueOf(v).Elem()
	rt := rv.Type()

	tagMap := make(map[string]int)
	nameMap := make(map[string]int)
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		if tag := f.Tag.Get("db"); tag != "" {
			tagMap[tag] = i
		}
		nameMap[f.Name] = i
	}

	ptrs := make([]any, len(columns))
	for i, col := range columns {
		if idx, ok := tagMap[col]; ok {
			ptrs[i] = rv.Field(idx).Addr().Interface()
		} else if idx, ok := nameMap[col]; ok {
			ptrs[i] = rv.Field(idx).Addr().Interface()
		} else {
			var discard any
			ptrs[i] = &discard
		}
	}
	return ptrs, nil
}
