package post

import (
	"io"
	"time"

	"github.com/mxk/go-sqlite/sqlite3"
)

// GetSimIds returns a list of all simulation ids in the cyclus database for
// conn.
func GetSimIds(conn *sqlite3.Conn) (ids [][]byte, err error) {
	sql := "SELECT SimID FROM Info"
	var stmt *sqlite3.Stmt
	for stmt, err = conn.Query(sql); err == nil; err = stmt.Next() {
		var s []byte
		if err := stmt.Scan(&s); err != nil {
			return nil, err
		}
		ids = append(ids, s)
	}
	if err != io.EOF {
		return nil, err
	}
	return ids, nil
}

func panicif(err error) {
	if err != nil {
		panic(err.Error())
	}
}

type Timer struct {
	starts map[string]time.Time
	Totals map[string]time.Duration
}

func NewTimer() *Timer {
	return &Timer{
		map[string]time.Time{},
		map[string]time.Duration{},
	}
}

func (t *Timer) Start(label string) {
	if _, ok := t.starts[label]; !ok {
		t.starts[label] = time.Now()
	}
}

func (t *Timer) Stop(label string) {
	if start, ok := t.starts[label]; ok {
		t.Totals[label] += time.Now().Sub(start)
	}
	delete(t.starts, label)
}

type NullWriter struct{}

func (_ NullWriter) Write(p []byte) (n int, err error) { return len(p), nil }