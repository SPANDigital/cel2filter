package googledrive_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/spandigital/cel2filter"
	"github.com/spandigital/cel2filter/dialect"
	"github.com/spandigital/cel2filter/dialect/googledrive"
)

// resolver knows two labels by title; discover makes it refuse "any label", as a source that learns label
// ids as it goes must.
type resolver struct{ discover bool }

var labelIDs = map[string]string{"Classification": "L1", "Archive": "L2"}

func (r resolver) Any() (string, []string, bool, bool) {
	if r.discover {
		return "", nil, false, false
	}
	return "('labels/L1' in labels) or ('labels/L2' in labels)", []string{"L1", "L2"}, true, true
}

func (r resolver) Entry(label, field string, value *string) (string, []string, bool, bool) {
	id, ok := labelIDs[label]
	if !ok {
		return "", nil, false, false
	}
	switch {
	case field == "":
		return "'labels/" + id + "' in labels", []string{id}, true, true
	case value == nil:
		return "labels/" + id + "." + field + " is not null", []string{id}, true, true
	}
	return "labels/" + id + "." + field + " = " + googledrive.Quote(*value), []string{id}, true, true
}

func opts(discover bool) []googledrive.Option {
	return []googledrive.Option{
		googledrive.WithTerm("title", "name", googledrive.String),
		googledrive.WithTerm("updated", "modifiedTime", googledrive.Time),
		googledrive.WithValues("type", func(v any) (string, bool, bool) {
			switch v {
			case "document":
				return "mimeType = 'application/vnd.google-apps.document'", true, true
			case "file":
				return "mimeType != 'application/vnd.google-apps.document'", false, true
			}
			return "", false, false
		}),
		googledrive.WithLabels("labels", resolver{discover: discover}),
	}
}

func TestRender(t *testing.T) {
	june := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name     string
		p        cel2filter.Predicate
		discover bool
		query    string
		labels   []string
		exact    bool
	}{
		{"nil", nil, false, "", nil, true},
		{"type", cel2filter.Eq{Field: "type", Value: "document"}, false, "mimeType = 'application/vnd.google-apps.document'", nil, true},
		{"documents with any label",
			cel2filter.And{Args: []cel2filter.Predicate{cel2filter.Eq{Field: "type", Value: "document"}, cel2filter.Exists{Field: "labels"}}}, false,
			"(mimeType = 'application/vnd.google-apps.document') and (('labels/L1' in labels) or ('labels/L2' in labels))", []string{"L1", "L2"}, true},
		{"discovery drops the label term",
			cel2filter.And{Args: []cel2filter.Predicate{cel2filter.Eq{Field: "type", Value: "document"}, cel2filter.Exists{Field: "labels"}}}, true,
			"mimeType = 'application/vnd.google-apps.document'", nil, false},
		{"label value", cel2filter.Eq{Field: "labels", Value: "Classification/Level=Confidential"}, false,
			"labels/L1.Level = 'Confidential'", []string{"L1"}, true},
		{"unknown label is dropped", cel2filter.Eq{Field: "labels", Value: "Nope"}, false, "", nil, false},
		{"name quoting", cel2filter.Eq{Field: "title", Value: `Bob's \ notes`}, false, `name = 'Bob\'s \\ notes'`, nil, true},
		{"prefix is contains, inexact", cel2filter.Prefix{Field: "title", Value: "Q4"}, false, "name contains 'Q4'", nil, false},
		{"time", cel2filter.Compare{Field: "updated", Op: cel2filter.Gt, Value: june}, false, "modifiedTime > '2026-06-01T00:00:00Z'", nil, true},
		{"fractional time widens", cel2filter.Compare{Field: "updated", Op: cel2filter.Gt, Value: june.Add(500 * time.Millisecond)}, false,
			"modifiedTime >= '2026-06-01T00:00:00Z'", nil, false},
		{"fractional upper bound widens", cel2filter.Compare{Field: "updated", Op: cel2filter.Lt, Value: june.Add(500 * time.Millisecond)}, false,
			"modifiedTime <= '2026-06-01T00:00:01Z'", nil, false},
		{"in", cel2filter.In{Field: "title", Values: []any{"a", "b"}}, false, "(name = 'a') or (name = 'b')", nil, true},
		{"or with an unmapped branch is true",
			cel2filter.Or{Args: []cel2filter.Predicate{cel2filter.Eq{Field: "title", Value: "a"}, cel2filter.Eq{Field: "author", Value: "x"}}}, false, "", nil, false},
		{"and drops the unmapped part",
			cel2filter.And{Args: []cel2filter.Predicate{cel2filter.Eq{Field: "title", Value: "a"}, cel2filter.Eq{Field: "author", Value: "x"}}}, false, "name = 'a'", nil, false},
		{"not over exact", cel2filter.Not{Arg: cel2filter.Eq{Field: "title", Value: "a"}}, false, "not (name = 'a')", nil, true},
		{"not over inexact is dropped", cel2filter.Not{Arg: cel2filter.Prefix{Field: "title", Value: "a"}}, false, "", nil, false},
		{"exists on a plain field is dropped", cel2filter.Exists{Field: "title"}, false, "", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := googledrive.Render(c.p, opts(c.discover)...)
			require.Equal(t, c.query, r.Query)
			require.Equal(t, c.labels, r.Labels)
			require.Equal(t, c.exact, r.Exact)
		})
	}
}

func TestRegistered(t *testing.T) {
	d, err := dialect.Get(dialect.GoogleDrive)
	require.NoError(t, err)
	out, _, err := d.Render(cel2filter.Eq{Field: "title", Value: "a"})
	require.NoError(t, err)
	require.Nil(t, out, "with no mappings every field is unmapped")
}
