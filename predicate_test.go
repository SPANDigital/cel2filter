package cel2filter_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/spandigital/cel2filter"
)

func TestPredicateString(t *testing.T) {
	ts := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	cases := map[string]cel2filter.Predicate{
		`kind == "leaf"`:                  cel2filter.Eq{Field: "kind", Value: "leaf"},
		`evergreen == true`:               cel2filter.Eq{Field: "evergreen", Value: true},
		`count == 3`:                      cel2filter.Eq{Field: "count", Value: int64(3)},
		`score == 2.5`:                    cel2filter.Eq{Field: "score", Value: 2.5},
		`category in ["a", "b"]`:          cel2filter.In{Field: "category", Values: []any{"a", "b"}},
		`path startsWith "career"`:        cel2filter.Prefix{Field: "path", Value: "career"},
		`has(author)`:                     cel2filter.Exists{Field: "author"},
		`count > 3`:                       cel2filter.Compare{Field: "count", Op: cel2filter.Gt, Value: int64(3)},
		`updated >= 2025-01-01T00:00:00Z`: cel2filter.Compare{Field: "updated", Op: cel2filter.Gte, Value: ts},
		`not(kind == "leaf")`:             cel2filter.Not{Arg: cel2filter.Eq{Field: "kind", Value: "leaf"}},
		`and(kind == "leaf", or(count < 1, count <= 2))`: cel2filter.And{Args: []cel2filter.Predicate{cel2filter.Eq{Field: "kind", Value: "leaf"}, cel2filter.Or{Args: []cel2filter.Predicate{cel2filter.Compare{Field: "count", Op: cel2filter.Lt, Value: int64(1)}, cel2filter.Compare{Field: "count", Op: cel2filter.Lte, Value: int64(2)}}}}},
	}
	for want, p := range cases {
		require.Equal(t, want, p.String())
	}
	require.Equal(t, "?", cel2filter.CompareOp(0).String())
}

func TestMatch(t *testing.T) {
	ts := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	doc := cel2filter.MapFields{
		"kind": "leaf", "tags": []string{"a", "b"}, "nums": []int64{1, 2}, "mixed": []any{"x", int64(3)},
		"count": int64(3), "score": 2.5, "small": int(2), "updated": ts, "flag": true, "empty": "", "none": []string{},
	}
	cases := []struct {
		p    cel2filter.Predicate
		want bool
	}{
		{nil, true},
		{cel2filter.Eq{Field: "kind", Value: "leaf"}, true},
		{cel2filter.Eq{Field: "kind", Value: "branch"}, false},
		{cel2filter.Eq{Field: "missing", Value: "x"}, false},
		{cel2filter.Eq{Field: "tags", Value: "b"}, true},
		{cel2filter.Eq{Field: "nums", Value: int64(2)}, true},
		{cel2filter.Eq{Field: "nums", Value: 2.0}, true},
		{cel2filter.Eq{Field: "mixed", Value: int64(3)}, true},
		{cel2filter.Eq{Field: "count", Value: 3.0}, true},
		{cel2filter.Eq{Field: "score", Value: int64(2)}, false},
		{cel2filter.Eq{Field: "small", Value: int64(2)}, true},
		{cel2filter.Eq{Field: "flag", Value: true}, true},
		{cel2filter.Eq{Field: "updated", Value: ts}, true},
		{cel2filter.Eq{Field: "kind", Value: 3}, false},
		{cel2filter.In{Field: "kind", Values: []any{"x", "leaf"}}, true},
		{cel2filter.In{Field: "tags", Values: []any{"z", "a"}}, true},
		{cel2filter.In{Field: "kind", Values: []any{"x"}}, false},
		{cel2filter.Prefix{Field: "kind", Value: "le"}, true},
		{cel2filter.Prefix{Field: "tags", Value: "a"}, false},
		{cel2filter.Exists{Field: "kind"}, true},
		{cel2filter.Exists{Field: "empty"}, false},
		{cel2filter.Exists{Field: "none"}, false},
		{cel2filter.Exists{Field: "missing"}, false},
		{cel2filter.Compare{Field: "count", Op: cel2filter.Gt, Value: int64(2)}, true},
		{cel2filter.Compare{Field: "count", Op: cel2filter.Gte, Value: 3.0}, true},
		{cel2filter.Compare{Field: "count", Op: cel2filter.Lt, Value: int64(3)}, false},
		{cel2filter.Compare{Field: "count", Op: cel2filter.Lte, Value: int64(3)}, true},
		{cel2filter.Compare{Field: "score", Op: cel2filter.Gt, Value: int64(2)}, true},
		{cel2filter.Compare{Field: "nums", Op: cel2filter.Gt, Value: int64(1)}, true},
		{cel2filter.Compare{Field: "updated", Op: cel2filter.Gt, Value: ts.Add(-time.Hour)}, true},
		{cel2filter.Compare{Field: "updated", Op: cel2filter.Gte, Value: ts}, true},
		{cel2filter.Compare{Field: "updated", Op: cel2filter.Lt, Value: ts}, false},
		{cel2filter.Compare{Field: "updated", Op: cel2filter.Lte, Value: ts}, true},
		{cel2filter.Compare{Field: "kind", Op: cel2filter.Gt, Value: int64(1)}, false},
		{cel2filter.Compare{Field: "missing", Op: cel2filter.Lt, Value: int64(1)}, false},
		{cel2filter.Compare{Field: "count", Op: cel2filter.Gt, Value: "x"}, false},
		{cel2filter.Not{Arg: cel2filter.Eq{Field: "kind", Value: "branch"}}, true},
		{cel2filter.And{Args: []cel2filter.Predicate{cel2filter.Eq{Field: "kind", Value: "leaf"}, cel2filter.Exists{Field: "empty"}}}, false},
		{cel2filter.Or{Args: []cel2filter.Predicate{cel2filter.Eq{Field: "kind", Value: "x"}, cel2filter.Exists{Field: "flag"}}}, true},
		{cel2filter.Or{}, false},
		{cel2filter.And{}, true},
	}
	for _, c := range cases {
		require.Equal(t, c.want, cel2filter.Match(c.p, doc), "%v", c.p)
	}
	require.True(t, cel2filter.IsZero(nil))
	require.True(t, cel2filter.IsZero(map[string]any{}))
	require.False(t, cel2filter.IsZero(struct{}{}))
}
