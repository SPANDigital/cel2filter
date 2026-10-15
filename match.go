package cel2filter

import (
	"strings"
	"time"
)

// Fields is a document as a store sees it: a lookup from store key to the stored value. Values are strings,
// bools, int64, float64, time.Time, or slices of those (a list field). Match treats an absent key like an empty
// one.
type Fields interface {
	Field(name string) (any, bool)
}

// MapFields is the simplest Fields: a map from store key to value.
type MapFields map[string]any

// Field returns the value stored under name.
func (m MapFields) Field(name string) (any, bool) {
	v, ok := m[name]
	return v, ok
}

// Match evaluates a predicate with the semantics the dialects implement: equality on scalars or any element of
// a list, presence as "set and non-empty", numeric and timestamp ordering, and plain negation. A nil predicate
// matches everything. It is the reference the dialect renderings are tested against; applications use it to
// filter in memory or to check a store's behaviour.
func Match(p Predicate, doc Fields) bool {
	switch p := p.(type) {
	case nil:
		return true
	case And:
		for _, a := range p.Args {
			if !Match(a, doc) {
				return false
			}
		}
		return true
	case Or:
		for _, a := range p.Args {
			if Match(a, doc) {
				return true
			}
		}
		return false
	case Not:
		return !Match(p.Arg, doc)
	case Eq:
		v, _ := doc.Field(p.Field)
		return anyElement(v, func(e any) bool { return equal(e, p.Value) })
	case In:
		v, _ := doc.Field(p.Field)
		return anyElement(v, func(e any) bool {
			for _, want := range p.Values {
				if equal(e, want) {
					return true
				}
			}
			return false
		})
	case Prefix:
		v, _ := doc.Field(p.Field)
		s, ok := v.(string)
		return ok && strings.HasPrefix(s, p.Value)
	case Exists:
		v, ok := doc.Field(p.Field)
		return ok && !IsZero(v)
	case Compare:
		v, _ := doc.Field(p.Field)
		return anyElement(v, func(e any) bool { return compare(e, p.Op, p.Value) })
	}
	return false
}

// IsZero reports whether a stored value counts as unset: nil, "", false, 0, the zero time, or an empty list or
// map. Stores omit such values, and has() on a document is false for them.
func IsZero(v any) bool {
	switch v := v.(type) {
	case nil:
		return true
	case string:
		return v == ""
	case bool:
		return !v
	case int:
		return v == 0
	case int64:
		return v == 0
	case float64:
		return v == 0
	case time.Time:
		return v.IsZero()
	case []string:
		return len(v) == 0
	case []any:
		return len(v) == 0
	case []int64:
		return len(v) == 0
	case []float64:
		return len(v) == 0
	case map[string]any:
		return len(v) == 0
	}
	return false
}

func anyElement(v any, f func(any) bool) bool {
	switch v := v.(type) {
	case []string:
		for _, e := range v {
			if f(e) {
				return true
			}
		}
		return false
	case []any:
		for _, e := range v {
			if f(e) {
				return true
			}
		}
		return false
	case []int64:
		for _, e := range v {
			if f(e) {
				return true
			}
		}
		return false
	case []float64:
		for _, e := range v {
			if f(e) {
				return true
			}
		}
		return false
	}
	return f(v)
}

func equal(stored, want any) bool {
	switch w := want.(type) {
	case string:
		s, ok := stored.(string)
		return ok && s == w
	case bool:
		b, ok := stored.(bool)
		return ok && b == w
	case int64, float64:
		return numeric(stored, func(s float64, si int64, isInt bool) bool {
			if wi, ok := w.(int64); ok {
				if isInt {
					return si == wi
				}
				return s == float64(wi)
			}
			return s == w.(float64)
		})
	case time.Time:
		t, ok := stored.(time.Time)
		return ok && t.Equal(w)
	}
	return false
}

func compare(stored any, op CompareOp, want any) bool {
	switch w := want.(type) {
	case int64, float64:
		return numeric(stored, func(s float64, si int64, isInt bool) bool {
			if wi, ok := w.(int64); ok && isInt {
				return ordered(si, wi, op)
			}
			var wf float64
			if wi, ok := w.(int64); ok {
				wf = float64(wi)
			} else {
				wf = w.(float64)
			}
			return ordered(s, wf, op)
		})
	case time.Time:
		t, ok := stored.(time.Time)
		if !ok {
			return false
		}
		switch op {
		case Gt:
			return t.After(w)
		case Gte:
			return !t.Before(w)
		case Lt:
			return t.Before(w)
		case Lte:
			return !t.After(w)
		}
	}
	return false
}

// numeric calls f with the stored number as both float64 and, when it is an integer, int64.
func numeric(stored any, f func(float float64, integer int64, isInt bool) bool) bool {
	switch s := stored.(type) {
	case int:
		return f(float64(s), int64(s), true)
	case int64:
		return f(float64(s), s, true)
	case float64:
		return f(s, 0, false)
	}
	return false
}

func ordered[T int64 | float64](a, b T, op CompareOp) bool {
	switch op {
	case Gt:
		return a > b
	case Gte:
		return a >= b
	case Lt:
		return a < b
	case Lte:
		return a <= b
	}
	return false
}
