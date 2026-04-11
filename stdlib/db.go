package stdlib

import (
	"database/sql"
	"fmt"
	"reflect"
)

// QueryAs executes a query and returns results as a slice of T.
// Uses struct tags (db:"column_name") for column-to-field mapping.
// If T implements Validatable, each row is validated after scanning.
func QueryAs[T any](db *sql.DB, query string, args ...any) ([]T, error) {
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
		var v T
		ptrs, err := structScanPtrs(&v, columns)
		if err != nil {
			return nil, err
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		if err := ArcaValidateIfPossible(&v); err != nil {
			return nil, fmt.Errorf("validation failed for row: %w", err)
		}
		results = append(results, v)
	}
	return results, rows.Err()
}

// QueryOneAs executes a query and returns a single result.
// Returns an error if no rows are found.
func QueryOneAs[T any](db *sql.DB, query string, args ...any) (T, error) {
	results, err := QueryAs[T](db, query, args...)
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

// structScanPtrs returns a slice of pointers to struct fields matching the given columns.
// Uses "db" struct tag for mapping, falls back to field name.
func structScanPtrs(v any, columns []string) ([]any, error) {
	rv := reflect.ValueOf(v).Elem()
	rt := rv.Type()

	// Build tag → field index map
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
			// Column not mapped — scan into discard
			var discard any
			ptrs[i] = &discard
		}
	}
	return ptrs, nil
}
