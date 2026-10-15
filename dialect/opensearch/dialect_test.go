package opensearch_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/spandigital/cel2filter"
	"github.com/spandigital/cel2filter/dialect"
	"github.com/spandigital/cel2filter/dialect/opensearch"
)

func render(t *testing.T, p cel2filter.Predicate, opts ...opensearch.Option) (string, bool) {
	t.Helper()
	q, exact, err := opensearch.Render(p, opts...)
	require.NoError(t, err)
	if q == nil {
		return "", exact
	}
	b, err := json.Marshal(q)
	require.NoError(t, err)
	return string(b), exact
}

func TestRender(t *testing.T) {
	eq := cel2filter.Eq{Field: "tags", Value: "ops"}
	ts := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	keyword := func(f string) bool { return f != "title" }
	cases := []struct {
		name  string
		p     cel2filter.Predicate
		opts  []opensearch.Option
		want  string
		exact bool
	}{
		{"nil", nil, nil, "", true},
		{"tags and author", cel2filter.And{Args: []cel2filter.Predicate{eq, cel2filter.Eq{Field: "author", Value: "x"}}}, nil,
			`{"bool":{"filter":[{"term":{"tags":"ops"}},{"term":{"author":"x"}}]}}`, true},
		{"label", cel2filter.Eq{Field: "labels", Value: "Classification/Level=Confidential"}, nil,
			`{"term":{"labels":"Classification/Level=Confidential"}}`, true},
		{"negated scalar", cel2filter.And{Args: []cel2filter.Predicate{eq, cel2filter.Not{Arg: cel2filter.Eq{Field: "kind", Value: "leaf"}}}}, nil,
			`{"bool":{"filter":[{"term":{"tags":"ops"}},{"bool":{"must_not":{"term":{"kind":"leaf"}}}}]}}`, true},
		{"in, prefix, exists, or", cel2filter.Or{Args: []cel2filter.Predicate{
			cel2filter.In{Field: "category", Values: []any{"a", "b"}}, cel2filter.Prefix{Field: "path", Value: "ops"}, cel2filter.Exists{Field: "author"}}}, nil,
			`{"bool":{"minimum_should_match":1,"should":[{"terms":{"category":["a","b"]}},{"prefix":{"path":"ops"}},{"exists":{"field":"author"}}]}}`, true},
		{"text field is not rendered", cel2filter.And{Args: []cel2filter.Predicate{eq, cel2filter.Eq{Field: "title", Value: "x"}}}, []opensearch.Option{opensearch.WithFilterable(keyword)},
			`{"term":{"tags":"ops"}}`, false},
		{"or over a text field is dropped", cel2filter.Or{Args: []cel2filter.Predicate{eq, cel2filter.Eq{Field: "title", Value: "x"}}}, []opensearch.Option{opensearch.WithFilterable(keyword)}, "", false},
		{"negation over a dropped leaf", cel2filter.Not{Arg: cel2filter.Eq{Field: "title", Value: "x"}}, []opensearch.Option{opensearch.WithFilterable(keyword)}, "", false},
		{"range numbers", cel2filter.And{Args: []cel2filter.Predicate{cel2filter.Compare{Field: "count", Op: cel2filter.Gt, Value: int64(3)}, cel2filter.Compare{Field: "score", Op: cel2filter.Lte, Value: 2.5}}}, nil,
			`{"bool":{"filter":[{"range":{"count":{"gt":3}}},{"range":{"score":{"lte":2.5}}}]}}`, true},
		{"range date", cel2filter.Compare{Field: "updated", Op: cel2filter.Gte, Value: ts}, nil, `{"range":{"updated":{"gte":"2025-01-01T00:00:00Z"}}}`, true},
		{"date equality", cel2filter.Eq{Field: "updated", Value: ts}, nil, `{"term":{"updated":"2025-01-01T00:00:00Z"}}`, true},
		{"strict lower bound widens", cel2filter.Compare{Field: "updated", Op: cel2filter.Gt, Value: ts}, nil, `{"range":{"updated":{"gte":"2025-01-01T00:00:00Z"}}}`, false},
		{"sub-millisecond lower bound floors", cel2filter.Compare{Field: "updated", Op: cel2filter.Gte, Value: ts.Add(500 * time.Microsecond)}, nil, `{"range":{"updated":{"gte":"2025-01-01T00:00:00Z"}}}`, false},
		{"sub-millisecond upper bound ceils", cel2filter.Compare{Field: "updated", Op: cel2filter.Lte, Value: ts.Add(500 * time.Microsecond)}, nil, `{"range":{"updated":{"lte":"2025-01-01T00:00:00.001Z"}}}`, false},
		{"millisecond bound is exact", cel2filter.Compare{Field: "updated", Op: cel2filter.Lt, Value: ts.Add(time.Millisecond)}, nil, `{"range":{"updated":{"lt":"2025-01-01T00:00:00.001Z"}}}`, true},
		{"sub-millisecond equality drops", cel2filter.Eq{Field: "updated", Value: ts.Add(time.Microsecond)}, nil, "", false},
		{"negation over an inexact clause drops", cel2filter.Not{Arg: cel2filter.Compare{Field: "updated", Op: cel2filter.Gt, Value: ts}}, nil, "", false},
		{"negation over an exact date clause", cel2filter.Not{Arg: cel2filter.Compare{Field: "updated", Op: cel2filter.Gte, Value: ts}}, nil, `{"bool":{"must_not":{"range":{"updated":{"gte":"2025-01-01T00:00:00Z"}}}}}`, true},
		{"negated range", cel2filter.Not{Arg: cel2filter.Compare{Field: "count", Op: cel2filter.Lt, Value: int64(3)}}, nil, `{"bool":{"must_not":{"range":{"count":{"lt":3}}}}}`, true},
		{"empty in", cel2filter.In{Field: "k", Values: nil}, nil, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, exact := render(t, c.p, c.opts...)
			require.Equal(t, c.want, got)
			require.Equal(t, c.exact, exact, "exact")
		})
	}
}

func TestDialectInterface(t *testing.T) {
	d, err := dialect.Get(dialect.OpenSearch)
	require.NoError(t, err)
	require.Equal(t, dialect.OpenSearch, d.Name())
	out, exact, err := d.Render(cel2filter.Exists{Field: "k"})
	require.NoError(t, err)
	require.True(t, exact)
	require.Equal(t, map[string]any{"exists": map[string]any{"field": "k"}}, out)
	_, _, err = d.Render(unknown{})
	require.ErrorIs(t, err, dialect.ErrUnsupportedFeature)
}

type unknown struct{ cel2filter.Predicate }
