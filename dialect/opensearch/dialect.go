// Package opensearch renders predicates as OpenSearch Query DSL clauses for a bool query's filter context:
// term, terms, prefix, exists, range and nested bool clauses (filter, should with minimum_should_match 1,
// must_not). Put the result in the filter array of your bool query; the clauses never score.
//
// The rendering assumes keyword or numeric or date mappings for the fields it names; analysed text fields
// should be excluded with WithFilterable. must_not matches documents that lack the field, which is what the
// expression means when it treats an absent field as its zero value, so every negated leaf is rendered.
// Timestamps are written as RFC 3339. A date field holds milliseconds, and a document's own timestamp is
// truncated on the way in, so a strict lower bound is widened to a non-strict one on the floored millisecond
// (a document a few microseconds past the bound would otherwise be missed), and any bound finer than a
// millisecond is floored or ceiled towards the wider side. Such clauses are inexact, and a negation over an
// inexact clause is dropped, because negating a superset gives a subset.
package opensearch

import (
	"fmt"
	"time"

	"github.com/spandigital/cel2filter"
	"github.com/spandigital/cel2filter/dialect"
)

// Option configures a Dialect.
type Option func(*Dialect)

// Dialect renders OpenSearch bool filter clauses.
type Dialect struct {
	filterable func(string) bool
}

// New returns a dialect; without options every field is filterable.
func New(opts ...Option) *Dialect {
	d := &Dialect{filterable: func(string) bool { return true }}
	for _, o := range opts {
		o(d)
	}
	return d
}

// WithFilterable limits rendering to the fields mapped as keyword, boolean, numeric or date; a leaf on any
// other field is treated as true.
func WithFilterable(f func(field string) bool) Option {
	return func(d *Dialect) {
		if f != nil {
			d.filterable = f
		}
	}
}

func init() {
	dialect.Register(dialect.OpenSearch, func() dialect.Dialect { return New() })
}

// Name returns dialect.OpenSearch.
func (d *Dialect) Name() dialect.Name { return dialect.OpenSearch }

// Render implements dialect.Dialect.
func (d *Dialect) Render(p cel2filter.Predicate) (map[string]any, bool, error) {
	if p == nil {
		return nil, true, nil
	}
	return d.render(p)
}

// Render is a convenience for New(opts...).Render(p).
func Render(p cel2filter.Predicate, opts ...Option) (map[string]any, bool, error) {
	return New(opts...).Render(p)
}

func (d *Dialect) render(p cel2filter.Predicate) (map[string]any, bool, error) {
	switch p := p.(type) {
	case cel2filter.And:
		exact := true
		var args []any
		for _, a := range p.Args {
			q, ex, err := d.render(a)
			if err != nil {
				return nil, false, err
			}
			exact = exact && ex
			if q != nil {
				args = append(args, q)
			}
		}
		switch len(args) {
		case 0:
			return nil, exact, nil
		case 1:
			return args[0].(map[string]any), exact, nil
		}
		return map[string]any{"bool": map[string]any{"filter": args}}, exact, nil
	case cel2filter.Or:
		exact := true
		args := make([]any, 0, len(p.Args))
		for _, a := range p.Args {
			q, ex, err := d.render(a)
			if err != nil {
				return nil, false, err
			}
			if q == nil {
				return nil, false, nil
			}
			exact = exact && ex
			args = append(args, q)
		}
		if len(args) == 1 {
			return args[0].(map[string]any), exact, nil
		}
		return map[string]any{"bool": map[string]any{"should": args, "minimum_should_match": 1}}, exact, nil
	case cel2filter.Not:
		q, exact, err := d.render(p.Arg)
		if err != nil || q == nil || !exact {
			return nil, false, err
		}
		return map[string]any{"bool": map[string]any{"must_not": q}}, true, nil
	case cel2filter.Eq:
		if d.filterable(p.Field) && wholeMillis(p.Value) {
			return map[string]any{"term": map[string]any{p.Field: value(p.Value)}}, true, nil
		}
	case cel2filter.In:
		if d.filterable(p.Field) && len(p.Values) > 0 {
			vs := make([]any, len(p.Values))
			for i, v := range p.Values {
				if !wholeMillis(v) {
					return nil, false, nil
				}
				vs[i] = value(v)
			}
			return map[string]any{"terms": map[string]any{p.Field: vs}}, true, nil
		}
	case cel2filter.Prefix:
		if d.filterable(p.Field) {
			return map[string]any{"prefix": map[string]any{p.Field: p.Value}}, true, nil
		}
	case cel2filter.Exists:
		if d.filterable(p.Field) {
			return map[string]any{"exists": map[string]any{"field": p.Field}}, true, nil
		}
	case cel2filter.Compare:
		if d.filterable(p.Field) {
			op, v, exact := bound(p.Op, p.Value)
			return map[string]any{"range": map[string]any{p.Field: map[string]any{opName(op): v}}}, exact, nil
		}
	default:
		return nil, false, fmt.Errorf("%w: predicate %T", dialect.ErrUnsupportedFeature, p)
	}
	return nil, false, nil
}

func opName(op cel2filter.CompareOp) string {
	switch op {
	case cel2filter.Gt:
		return "gt"
	case cel2filter.Gte:
		return "gte"
	case cel2filter.Lt:
		return "lt"
	}
	return "lte"
}

func value(v any) any {
	if t, ok := v.(time.Time); ok {
		return t.UTC().Format(time.RFC3339Nano)
	}
	return v
}

// wholeMillis reports whether a value can be matched exactly by a date field.
func wholeMillis(v any) bool {
	t, ok := v.(time.Time)
	return !ok || t.Truncate(time.Millisecond).Equal(t)
}

// bound renders a comparison bound for a date field (numbers pass through). Lower bounds floor to the
// millisecond and a strict one becomes non-strict, since the document's own timestamp was floored too; upper
// bounds ceil. The clause is exact only for a non-strict lower bound or an upper bound on a whole millisecond.
func bound(op cel2filter.CompareOp, v any) (cel2filter.CompareOp, any, bool) {
	t, ok := v.(time.Time)
	if !ok {
		return op, v, true
	}
	ms := t.Truncate(time.Millisecond)
	whole := ms.Equal(t)
	switch op {
	case cel2filter.Gt:
		return cel2filter.Gte, value(ms), false
	case cel2filter.Gte:
		return op, value(ms), whole
	}
	if !whole {
		ms = ms.Add(time.Millisecond)
	}
	return op, value(ms), whole
}
