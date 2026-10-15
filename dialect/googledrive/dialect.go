// Package googledrive renders predicates as Google Drive files.list queries (the q parameter): name,
// mimeType and modifiedTime terms, label terms ('labels/ID' in labels, labels/ID.FIELD = 'x'), joined with
// and, or and not. See https://developers.google.com/workspace/drive/api/guides/ref-search-terms and
// https://developers.google.com/workspace/drive/api/guides/search-labels.
//
// Predicate fields mean nothing to Drive by themselves, so every field is mapped by an option: WithTerm for a
// field that is a Drive string or time term, WithValues for a field whose values each stand for a query
// fragment, and WithLabels for a flatkeys-encoded labels field, resolved to label, field and choice ids by
// the caller. A node on an unmapped field is treated as true, as in every dialect, and clears Result.Exact.
//
// Drive has no term for "has any label"; a LabelResolver answers Exists on the labels field with an OR over
// every label id it knows, and must refuse when it cannot know them all.
package googledrive

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/spandigital/cel2filter"
	"github.com/spandigital/cel2filter/dialect"
	"github.com/spandigital/cel2filter/flatkeys"
)

// TermKind is how a Drive term's values are written.
type TermKind uint8

const (
	// String terms (name, mimeType) take quoted strings: =, != and contains.
	String TermKind = iota + 1
	// Time terms (modifiedTime, createdTime) take quoted RFC 3339 times and compare.
	Time
)

// LabelResolver turns flatkeys entries of a labels field into Drive label terms. Each method returns the
// query fragment, the label ids it uses (the caller passes them to includeLabels), whether the fragment
// matches exactly the documents the entry describes, and false when it cannot be expressed.
type LabelResolver interface {
	// Any is "has at least one label": an OR over every label id, only possible when all are known.
	Any() (query string, ids []string, exact, ok bool)
	// Entry is one flatkeys entry: a label (field == ""), a field with a value (value == nil), or one value.
	Entry(label, field string, value *string) (query string, ids []string, exact, ok bool)
}

type mapping struct {
	term   string
	kind   TermKind
	values func(any) (string, bool, bool)
	labels LabelResolver
}

// Option configures a Dialect.
type Option func(*Dialect)

// Dialect renders Drive queries.
type Dialect struct {
	fields map[string]mapping
}

// Result is a rendered query. Query is "" when nothing could be expressed (no constraint). Labels lists the
// label ids the query uses, sorted.
type Result struct {
	Query  string
	Labels []string
	Exact  bool
}

// New returns a dialect with the given field mappings.
func New(opts ...Option) *Dialect {
	d := &Dialect{fields: map[string]mapping{}}
	for _, o := range opts {
		o(d)
	}
	return d
}

// WithTerm maps a predicate field to a Drive term such as name, mimeType or modifiedTime.
func WithTerm(field, term string, kind TermKind) Option {
	return func(d *Dialect) { d.fields[field] = mapping{term: term, kind: kind} }
}

// WithValues maps each value of a predicate field to a query fragment, for fields Drive has no term for but
// whose values it can test another way (a document type as a mimeType, say). f returns the fragment, whether
// it is exact, and false for a value it cannot express.
func WithValues(field string, f func(value any) (query string, exact, ok bool)) Option {
	return func(d *Dialect) { d.fields[field] = mapping{values: f} }
}

// WithLabels maps a flatkeys-encoded predicate field (see package flatkeys) to Drive labels through r.
func WithLabels(field string, r LabelResolver) Option {
	return func(d *Dialect) { d.fields[field] = mapping{labels: r} }
}

func init() {
	dialect.Register(dialect.GoogleDrive, func() dialect.Dialect { return New() })
}

// Name returns dialect.GoogleDrive.
func (d *Dialect) Name() dialect.Name { return dialect.GoogleDrive }

// Render implements dialect.Dialect; the query is returned as {"q": query, "includeLabels": [ids]}.
func (d *Dialect) Render(p cel2filter.Predicate) (map[string]any, bool, error) {
	r := d.RenderQuery(p)
	if r.Query == "" {
		return nil, r.Exact, nil
	}
	return map[string]any{"q": r.Query, "includeLabels": r.Labels}, r.Exact, nil
}

// RenderQuery renders p as a Drive query.
func (d *Dialect) RenderQuery(p cel2filter.Predicate) Result {
	if p == nil {
		return Result{Exact: true}
	}
	ids := map[string]bool{}
	q, exact := d.render(p, ids)
	return Result{Query: q, Labels: slices.Sorted(maps.Keys(ids)), Exact: exact}
}

// Render is a convenience for New(opts...).RenderQuery(p).
func Render(p cel2filter.Predicate, opts ...Option) Result {
	return New(opts...).RenderQuery(p)
}

// render returns the fragment for p ("" when p is treated as true) and whether it is exact.
func (d *Dialect) render(p cel2filter.Predicate, ids map[string]bool) (string, bool) {
	switch p := p.(type) {
	case cel2filter.And:
		exact := true
		var parts []string
		for _, a := range p.Args {
			q, ex := d.render(a, ids)
			exact = exact && ex
			if q != "" {
				parts = append(parts, q)
			}
		}
		return join(parts, "and"), exact
	case cel2filter.Or:
		exact := true
		parts := make([]string, 0, len(p.Args))
		used := map[string]bool{}
		for _, a := range p.Args {
			q, ex := d.render(a, used)
			if q == "" {
				return "", false // a branch treated as true makes the whole disjunction true
			}
			exact = exact && ex
			parts = append(parts, q)
		}
		for id := range used {
			ids[id] = true
		}
		return join(parts, "or"), exact
	case cel2filter.Not:
		used := map[string]bool{}
		q, exact := d.render(p.Arg, used)
		if q == "" || !exact {
			return "", false // negating a superset is not a superset of the negation
		}
		for id := range used {
			ids[id] = true
		}
		return "not " + paren(q), true
	case cel2filter.Eq:
		return d.leaf(p.Field, p.Value, ids)
	case cel2filter.In:
		parts := make([]string, 0, len(p.Values))
		exact := true
		used := map[string]bool{}
		for _, v := range p.Values {
			q, ex := d.leaf(p.Field, v, used)
			if q == "" {
				return "", false
			}
			exact = exact && ex
			parts = append(parts, q)
		}
		for id := range used {
			ids[id] = true
		}
		return join(parts, "or"), exact
	case cel2filter.Prefix:
		m, ok := d.fields[p.Field]
		if !ok || m.kind != String {
			return "", false
		}
		// contains matches a prefix of any word in name and a substring of mimeType: a superset either way.
		return fmt.Sprintf("%s contains %s", m.term, quote(p.Value)), false
	case cel2filter.Compare:
		return d.compare(p)
	case cel2filter.Exists:
		m, ok := d.fields[p.Field]
		if !ok || m.labels == nil {
			return "", false
		}
		q, used, exact, ok := m.labels.Any()
		if !ok || q == "" {
			return "", false
		}
		for _, id := range used {
			ids[id] = true
		}
		return q, exact
	}
	return "", false
}

func (d *Dialect) leaf(field string, value any, ids map[string]bool) (string, bool) {
	m, ok := d.fields[field]
	if !ok {
		return "", false
	}
	switch {
	case m.values != nil:
		q, exact, ok := m.values(value)
		if !ok {
			return "", false
		}
		return q, exact
	case m.labels != nil:
		entry, isString := value.(string)
		if !isString {
			return "", false
		}
		label, f, v, ok := flatkeys.Parse(entry)
		if !ok {
			return "", false
		}
		q, used, exact, ok := m.labels.Entry(label, f, v)
		if !ok || q == "" {
			return "", false
		}
		for _, id := range used {
			ids[id] = true
		}
		return q, exact
	case m.kind == String:
		s, isString := value.(string)
		if !isString {
			return "", false
		}
		return fmt.Sprintf("%s = %s", m.term, quote(s)), true
	case m.kind == Time:
		t, isTime := value.(time.Time)
		if !isTime {
			return "", false
		}
		if t.Truncate(time.Second) != t {
			return "", false // Drive compares whole seconds; equality with a fractional time cannot be written
		}
		return fmt.Sprintf("%s = %s", m.term, quote(t.UTC().Format(time.RFC3339))), true
	}
	return "", false
}

// compare renders a time comparison. Drive takes whole seconds, so a fractional bound is widened to the
// enclosing second (> 10.5s becomes >= 10s) and the result marked inexact.
func (d *Dialect) compare(p cel2filter.Compare) (string, bool) {
	m, ok := d.fields[p.Field]
	if !ok || m.kind != Time {
		return "", false
	}
	t, isTime := p.Value.(time.Time)
	if !isTime {
		return "", false
	}
	t = t.UTC()
	op, exact := p.Op, true
	if floor := t.Truncate(time.Second); floor != t {
		exact = false
		switch op {
		case cel2filter.Gt, cel2filter.Gte:
			op, t = cel2filter.Gte, floor
		case cel2filter.Lt, cel2filter.Lte:
			op, t = cel2filter.Lte, floor.Add(time.Second)
		}
	}
	sym := map[cel2filter.CompareOp]string{cel2filter.Gt: ">", cel2filter.Gte: ">=", cel2filter.Lt: "<", cel2filter.Lte: "<="}[op]
	if sym == "" {
		return "", false
	}
	return fmt.Sprintf("%s %s %s", m.term, sym, quote(t.Format(time.RFC3339))), exact
}

// Quote renders s as a Drive query string literal.
func Quote(s string) string { return quote(s) }

func quote(s string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s) + "'"
}

func paren(q string) string { return "(" + q + ")" }

func join(parts []string, op string) string {
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	}
	wrapped := make([]string, len(parts))
	for i, p := range parts {
		wrapped[i] = paren(p)
	}
	return strings.Join(wrapped, " "+op+" ")
}
