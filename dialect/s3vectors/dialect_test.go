package s3vectors_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/spandigital/cel2filter"
	"github.com/spandigital/cel2filter/dialect"
	"github.com/spandigital/cel2filter/dialect/s3vectors"
)

func render(t *testing.T, p cel2filter.Predicate, opts ...s3vectors.Option) (string, s3vectors.Result) {
	t.Helper()
	r, err := s3vectors.Render(p, opts...)
	require.NoError(t, err)
	if r.Filter == nil {
		return "", r
	}
	b, err := json.Marshal(r.Filter)
	require.NoError(t, err)
	return string(b), r
}

func TestRender(t *testing.T) {
	eq := cel2filter.Eq{Field: "tags", Value: "ops"}
	notLeaf := cel2filter.Not{Arg: cel2filter.Eq{Field: "kind", Value: "leaf"}}
	textOnly := func(f string) bool { return f != "title" }
	cases := []struct {
		name  string
		p     cel2filter.Predicate
		opts  []s3vectors.Option
		want  string
		n     int
		exact bool
	}{
		{"nil", nil, nil, "", 0, true},
		{"tags and author", cel2filter.And{Args: []cel2filter.Predicate{eq, cel2filter.Eq{Field: "author", Value: "x"}}}, nil,
			`{"$and":[{"tags":{"$eq":"ops"}},{"author":{"$eq":"x"}}]}`, 2, true},
		{"label", cel2filter.Eq{Field: "labels", Value: "Classification/Level=Confidential"}, nil,
			`{"labels":{"$eq":"Classification/Level=Confidential"}}`, 1, true},
		{"in, prefix, exists, or", cel2filter.Or{Args: []cel2filter.Predicate{
			cel2filter.In{Field: "category", Values: []any{"a", "b"}}, cel2filter.Prefix{Field: "path", Value: "ops"}, cel2filter.Exists{Field: "author"}}}, nil,
			`{"$or":[{"category":{"$in":["a","b"]}},{"path":{"$startsWith":"ops"}},{"author":{"$exists":true}}]}`, 4, true},
		{"negation dropped by default", cel2filter.And{Args: []cel2filter.Predicate{eq, notLeaf}}, nil, `{"tags":{"$eq":"ops"}}`, 1, false},
		{"negation on", cel2filter.And{Args: []cel2filter.Predicate{eq, notLeaf, cel2filter.Not{Arg: cel2filter.In{Field: "k", Values: []any{"a"}}}}}, []s3vectors.Option{s3vectors.WithNegation(true)},
			`{"$and":[{"tags":{"$eq":"ops"}},{"kind":{"$ne":"leaf"}},{"k":{"$nin":["a"]}}]}`, 3, true},
		{"negated prefix never renders", cel2filter.Not{Arg: cel2filter.Prefix{Field: "path", Value: "x"}}, []s3vectors.Option{s3vectors.WithNegation(true)}, "", 0, false},
		{"non-filterable field dropped", cel2filter.And{Args: []cel2filter.Predicate{eq, cel2filter.Eq{Field: "title", Value: "x"}}}, []s3vectors.Option{s3vectors.WithFilterable(textOnly)},
			`{"tags":{"$eq":"ops"}}`, 1, false},
		{"or with a non-filterable side dropped", cel2filter.Or{Args: []cel2filter.Predicate{eq, cel2filter.Eq{Field: "title", Value: "x"}}}, []s3vectors.Option{s3vectors.WithFilterable(textOnly)}, "", 0, false},
		{"single survivor unwrapped", cel2filter.Or{Args: []cel2filter.Predicate{eq}}, nil, `{"tags":{"$eq":"ops"}}`, 1, true},
		{"bool", cel2filter.Eq{Field: "evergreen", Value: true}, nil, `{"evergreen":{"$eq":true}}`, 1, true},
		{"numbers", cel2filter.And{Args: []cel2filter.Predicate{
			cel2filter.Compare{Field: "count", Op: cel2filter.Gt, Value: int64(3)}, cel2filter.Compare{Field: "score", Op: cel2filter.Lte, Value: 2.5},
			cel2filter.Compare{Field: "n", Op: cel2filter.Gte, Value: int64(1)}, cel2filter.Compare{Field: "n", Op: cel2filter.Lt, Value: int64(9)}}}, nil,
			`{"$and":[{"count":{"$gt":3}},{"score":{"$lte":2.5}},{"n":{"$gte":1}},{"n":{"$lt":9}}]}`, 4, true},
		{"timestamps drop", cel2filter.And{Args: []cel2filter.Predicate{eq, cel2filter.Compare{Field: "updated", Op: cel2filter.Gt, Value: time.Now()}, cel2filter.Eq{Field: "updated", Value: time.Now()}}}, nil,
			`{"tags":{"$eq":"ops"}}`, 1, false},
		{"empty in", cel2filter.In{Field: "k", Values: nil}, nil, "", 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, r := render(t, c.p, c.opts...)
			require.Equal(t, c.want, got)
			require.Equal(t, c.n, r.Constraints, "constraints")
			require.Equal(t, c.exact, r.Exact, "exact")
		})
	}
}

func TestConstraintLimit(t *testing.T) {
	many := make([]any, 101)
	for i := range many {
		many[i] = int64(i)
	}
	p := cel2filter.In{Field: "category", Values: many}
	r, err := s3vectors.Render(p, s3vectors.WithMaxConstraints(100))
	require.ErrorIs(t, err, s3vectors.ErrConstraintLimit)
	require.Equal(t, 101, r.Constraints)
	require.Nil(t, r.Filter)
	r, err = s3vectors.Render(p)
	require.NoError(t, err)
	require.Equal(t, 101, r.Constraints)
}

func TestDialectInterface(t *testing.T) {
	d, err := dialect.Get(dialect.S3Vectors)
	require.NoError(t, err)
	require.Equal(t, dialect.S3Vectors, d.Name())
	out, exact, err := d.Render(cel2filter.Eq{Field: "k", Value: "v"})
	require.NoError(t, err)
	require.True(t, exact)
	require.Equal(t, map[string]any{"k": map[string]any{"$eq": "v"}}, out)
	out, exact, err = d.Render(nil)
	require.NoError(t, err)
	require.True(t, exact)
	require.Nil(t, out)
	require.Contains(t, dialect.Registered(), dialect.S3Vectors)
	_, err = dialect.Get("nope")
	require.ErrorIs(t, err, dialect.ErrUnsupportedFeature)
	_, _, err = d.Render(unknown{})
	require.ErrorIs(t, err, dialect.ErrUnsupportedFeature)
}

type unknown struct{ cel2filter.Predicate }
