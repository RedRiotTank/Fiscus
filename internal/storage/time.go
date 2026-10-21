package storage

import (
	"database/sql"
	"fmt"
	"time"
)

// storedTimeLayout is fixed-width so that lexicographic ordering of stored
// timestamps matches chronological ordering. All timestamps are stored in UTC.
const storedTimeLayout = "2006-01-02T15:04:05.000000000Z"

func nowUTC() time.Time {
	return time.Now().UTC()
}

func encodeTime(t time.Time) string {
	return t.UTC().Format(storedTimeLayout)
}

func decodeTime(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse stored time %q: %w", s, err)
	}
	return t.UTC(), nil
}

func decodeNullTime(v sql.NullString) (*time.Time, error) {
	if !v.Valid || v.String == "" {
		return nil, nil
	}
	t, err := decodeTime(v.String)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func encodeNullTime(t *time.Time) any {
	if t == nil || t.IsZero() {
		return nil
	}
	return encodeTime(*t)
}
