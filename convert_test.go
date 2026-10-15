package cel2filter_test

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"cel.dev/cel-go/cel"
	"cel.dev/cel-go/ext"
	"github.com/stretchr/testify/require"

	"github.com/spandigital/cel2filter"
	"github.com/spandigital/cel2filter/flatkeys"
	"github.com/spandigital/cel2filter/schema"
)

// docSchema is the document the tests are written against: a documentation corpus with front matter, plus a
// count, a score and a nested object.
var docSchema = schema.NewSchema([]schema.FieldSchema{
	{Name: "source", Type: cel.StringType}, {Name: "module", Type: cel.StringType}, {Name: "key", Type: cel.StringType},
	{Name: "path", Type: cel.StringType}, {Name: "title", Type: cel.StringType}, {Name: "section", Type: cel.StringType},
	{Name: "url", Type: cel.StringType}, {Name: "updated", Type: cel.TimestampType},
	{Name: "roles", Type: cel.StringType, Repeated: true}, {Name: "tags", Type: cel.StringType, Repeated: true},
	{Name: "scope", Type: cel.StringType, Repeated: true}, {Name: "acl", Type: cel.StringType, Repeated: true},
	{Name: "author", Type: cel.StringType}, {Name: "category", Type: cel.StringType}, {Name: "kind", Type: cel.StringType},
	{Name: "slug", Type: cel.StringType}, {Name: "summary", Type: cel.StringType}, {Name: "evergreen", Type: cel.BoolType},
	{Name: "labels", Type: cel.MapType(cel.StringType, cel.MapType(cel.StringType, cel.DynType))},
	{Name: "count", Type: cel.IntType}, {Name: "score", Type: cel.DoubleType},
	{Name: "meta", Fields: []schema.FieldSchema{{Name: "owner", Type: cel.StringType}, {Name: "n", Type: cel.IntType}}},
})

func newEnv(t testing.TB, extra ...cel.EnvOption) *cel.Env {
	t.Helper()
	opts := append(schema.EnvOptions("doc", "Doc", docSchema), cel.OptionalTypes(), ext.Strings(), cel.DefaultUTCTimeZone(true), cel.CrossTypeNumericComparisons(true))
	env, err := cel.NewEnv(append(opts, extra...)...)
	require.NoError(t, err)
	return env
}

func compile(t testing.TB, env *cel.Env, expr string) *cel.Ast {
	t.Helper()
	ast, issues := env.Compile(expr)
	require.NoError(t, issues.Err(), expr)
	return ast
}

var defaultOpts = []cel2filter.ConvertOption{cel2filter.WithRoot("doc"), cel2filter.WithEncoder("labels", flatkeys.Encoder{})}

func convert(t testing.TB, env *cel.Env, expr string, opts ...cel2filter.ConvertOption) cel2filter.Result {
	t.Helper()
	if opts == nil {
		opts = defaultOpts
	}
	res, err := cel2filter.Convert(compile(t, env, expr), opts...)
	require.NoError(t, err, expr)
	return res
}

func TestConvert(t *testing.T) {
	env := newEnv(t)
	ts := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	eq := func(f string, v any) cel2filter.Predicate { return cel2filter.Eq{Field: f, Value: v} }
	cases := []struct {
		expr  string
		want  cel2filter.Predicate
		exact bool
	}{
		// equality, membership, prefix, presence
		{`"onboarding" in doc.tags`, eq("tags", "onboarding"), true},
		{`doc.kind == "leaf"`, eq("kind", "leaf"), true},
		{`"leaf" == doc.kind`, eq("kind", "leaf"), true},
		{`doc.evergreen == true`, eq("evergreen", true), true},
		{`doc.evergreen`, eq("evergreen", true), true},
		{`!doc.evergreen`, cel2filter.Not{Arg: eq("evergreen", true)}, true},
		{`doc.category in ["policy", "process"]`, cel2filter.In{Field: "category", Values: []any{"policy", "process"}}, true},
		{`doc.path.startsWith("career")`, cel2filter.Prefix{Field: "path", Value: "career"}, true},
		{`doc.path.startsWith("")`, nil, true},
		{`has(doc.author)`, cel2filter.Exists{Field: "author"}, true},
		{`has(doc.tags)`, cel2filter.Exists{Field: "tags"}, true},
		{`has(doc.labels)`, cel2filter.Exists{Field: "labels"}, true},
		{`size(doc.labels) > 0`, cel2filter.Exists{Field: "labels"}, true},
		{`doc.labels.size() >= 1`, cel2filter.Exists{Field: "labels"}, true},
		{`size(doc.tags) != 0`, cel2filter.Exists{Field: "tags"}, true},
		{`0 < size(doc.tags)`, cel2filter.Exists{Field: "tags"}, true},
		{`size(doc.tags) > 2`, cel2filter.Exists{Field: "tags"}, false},
		{`size(doc.tags) == 3`, cel2filter.Exists{Field: "tags"}, false},
		{`3 <= size(doc.tags)`, cel2filter.Exists{Field: "tags"}, false},
		{`size(doc.tags) == 0`, nil, false},
		{`size(doc.tags) < 3`, nil, false},
		{`size(doc.tags) >= 0`, nil, false},
		{`!(size(doc.tags) > 0)`, nil, false},
		{`!(size(doc.tags) == 0)`, cel2filter.Exists{Field: "tags"}, true},
		{`size(doc.title) > 0`, nil, false},
		{`size(doc.labels.Classification) > 0`, nil, false},
		// encoded map lookups
		{`has(doc.labels.Classification)`, eq("labels", "Classification"), true},
		{`"Classification" in doc.labels`, eq("labels", "Classification"), true},
		{`has(doc.labels.Classification.Level)`, eq("labels", "Classification/Level"), true},
		{`has(doc.labels.Classification.Level.x)`, nil, false},
		{`doc.labels["Classification"]["Level"] == "Confidential"`, eq("labels", "Classification/Level=Confidential"), false},
		{`doc.labels.Classification.Level == "Confidential"`, eq("labels", "Classification/Level=Confidential"), false},
		{`"bob" in doc.labels.Classification.Owners`, eq("labels", "Classification/Owners=bob"), false},
		{`doc.labels["A/B"]["C=D"] == "x/y=z"`, eq("labels", `A\/B/C\=D=x/y=z`), false},
		{`doc.labels.Classification.Level in ["Public", "Internal"]`, cel2filter.In{Field: "labels", Values: []any{"Classification/Level=Public", "Classification/Level=Internal"}}, false},
		{`doc.labels.Classification.Count == 3`, eq("labels", "Classification/Count=3"), false},
		{`doc.labels.Classification.Count > 3`, nil, false},
		{`!(doc.labels.Classification.Level == "x")`, nil, false},
		{`doc.labels.Classification.Level != "x"`, nil, false},
		// conjunction and disjunction
		{`doc.kind == "leaf" && "x" in doc.tags`, cel2filter.And{Args: []cel2filter.Predicate{eq("kind", "leaf"), eq("tags", "x")}}, true},
		{`doc.kind == "leaf" && doc.title.matches("x")`, eq("kind", "leaf"), false},
		{`doc.kind == "leaf" || doc.title.matches("x")`, nil, false},
		{`doc.kind == "leaf" || "x" in doc.tags`, cel2filter.Or{Args: []cel2filter.Predicate{eq("kind", "leaf"), eq("tags", "x")}}, true},
		{`doc.kind == "leaf" || true`, nil, true},
		{`true`, nil, true},
		{`false`, nil, false},
		{`doc.kind == "leaf" && true`, eq("kind", "leaf"), true},
		// negation normal form
		{`doc.kind != "leaf"`, cel2filter.Not{Arg: eq("kind", "leaf")}, true},
		{`!(doc.kind == "leaf")`, cel2filter.Not{Arg: eq("kind", "leaf")}, true},
		{`!(doc.category in ["a"])`, cel2filter.Not{Arg: cel2filter.In{Field: "category", Values: []any{"a"}}}, true},
		{`!("x" in doc.tags)`, nil, false},
		{`!has(doc.author)`, nil, false},
		{`!!(doc.kind == "leaf")`, eq("kind", "leaf"), true},
		{`!(doc.kind == "a" || doc.kind == "b")`, cel2filter.And{Args: []cel2filter.Predicate{cel2filter.Not{Arg: eq("kind", "a")}, cel2filter.Not{Arg: eq("kind", "b")}}}, true},
		{`!(doc.kind == "a" || doc.title.matches("x"))`, cel2filter.Not{Arg: eq("kind", "a")}, false},
		{`!(doc.kind == "a" && doc.kind == "b")`, cel2filter.Or{Args: []cel2filter.Predicate{cel2filter.Not{Arg: eq("kind", "a")}, cel2filter.Not{Arg: eq("kind", "b")}}}, true},
		{`!(doc.kind == "a" && doc.title.matches("x"))`, nil, false},
		{`doc.author == "x" && !(doc.kind == "a" || "t" in doc.tags)`, cel2filter.And{Args: []cel2filter.Predicate{eq("author", "x"), cel2filter.Not{Arg: eq("kind", "a")}}}, false},
		{`!doc.path.startsWith("x")`, cel2filter.Not{Arg: cel2filter.Prefix{Field: "path", Value: "x"}}, true},
		// typed comparisons
		{`doc.count > 3`, cel2filter.Compare{Field: "count", Op: cel2filter.Gt, Value: int64(3)}, true},
		{`doc.count >= 3`, cel2filter.Compare{Field: "count", Op: cel2filter.Gte, Value: int64(3)}, true},
		{`3 < doc.count`, cel2filter.Compare{Field: "count", Op: cel2filter.Gt, Value: int64(3)}, true},
		{`3 >= doc.count`, nil, false}, // count <= 3 is true for an absent field
		{`-1 >= doc.count`, cel2filter.Compare{Field: "count", Op: cel2filter.Lte, Value: int64(-1)}, true},
		{`doc.count < -1`, cel2filter.Compare{Field: "count", Op: cel2filter.Lt, Value: int64(-1)}, true},
		{`doc.count <= -1`, cel2filter.Compare{Field: "count", Op: cel2filter.Lte, Value: int64(-1)}, true},
		{`doc.count < 3`, nil, false},
		{`doc.count > -(5)`, nil, false},
		{`doc.count > 2.5`, cel2filter.Compare{Field: "count", Op: cel2filter.Gte, Value: int64(3)}, true},
		{`doc.count >= 2.5`, cel2filter.Compare{Field: "count", Op: cel2filter.Gte, Value: int64(3)}, true},
		{`doc.count < -2.5`, cel2filter.Compare{Field: "count", Op: cel2filter.Lte, Value: int64(-3)}, true},
		{`doc.count > 3.0`, cel2filter.Compare{Field: "count", Op: cel2filter.Gt, Value: int64(3)}, true},
		{`doc.score > 1`, cel2filter.Compare{Field: "score", Op: cel2filter.Gt, Value: 1.0}, true},
		{`doc.score >= 1.5`, cel2filter.Compare{Field: "score", Op: cel2filter.Gte, Value: 1.5}, true},
		{`doc.score < 1.5`, nil, false},
		{`!(doc.count > 3)`, cel2filter.Not{Arg: cel2filter.Compare{Field: "count", Op: cel2filter.Gt, Value: int64(3)}}, true},
		{`doc.count != 3`, cel2filter.Not{Arg: eq("count", int64(3))}, true},
		{`doc.count == 3`, eq("count", int64(3)), true},
		{`doc.count == 0`, nil, false},
		{`doc.kind == ""`, nil, false},
		{`doc.kind in ["", "x"]`, nil, false},
		{`doc.evergreen == false`, nil, false},
		{`doc.updated > timestamp("2025-01-01T00:00:00Z")`, cel2filter.Compare{Field: "updated", Op: cel2filter.Gt, Value: ts}, true},
		{`timestamp("2025-01-01T00:00:00Z") <= doc.updated`, cel2filter.Compare{Field: "updated", Op: cel2filter.Gte, Value: ts}, true},
		{`doc.updated < timestamp("2025-01-01T00:00:00Z")`, nil, false},
		{`doc.updated == timestamp(1735689600)`, eq("updated", ts), true},
		{`doc.updated > timestamp("not a time")`, nil, false},
		{`doc.title > "a"`, nil, false},
		// not pushable
		{`doc.title == doc.section`, nil, false},
		{`doc.labels[?"Classification"][?"Level"].orValue("") == "Confidential"`, nil, false},
		{`doc.tags.exists(t, t == "x")`, nil, false},
		{`size(doc.tags) == 1`, cel2filter.Exists{Field: "tags"}, false},
		{`doc.tags == ["x"]`, nil, false},
		{`doc.tags[0] == "x"`, nil, false},
		{`doc.count in []`, nil, false},
		{`doc.count + 1 > 3`, nil, false},
		// nested objects
		{`doc.meta.owner == "x"`, eq("meta.owner", "x"), true},
		{`doc.meta.n > 1`, cel2filter.Compare{Field: "meta.n", Op: cel2filter.Gt, Value: int64(1)}, true},
		{`has(doc.meta.owner)`, cel2filter.Exists{Field: "meta.owner"}, true},
		{`has(doc.meta)`, cel2filter.Exists{Field: "meta"}, true},
		// the combined corpus case
		{`doc.labels["Classification"]["Level"] == "Confidential" && doc.updated > timestamp("2025-01-01T00:00:00Z") && (doc.kind == "a" || doc.kind == "b")`,
			cel2filter.And{Args: []cel2filter.Predicate{eq("labels", "Classification/Level=Confidential"), cel2filter.Compare{Field: "updated", Op: cel2filter.Gt, Value: ts}, cel2filter.Or{Args: []cel2filter.Predicate{eq("kind", "a"), eq("kind", "b")}}}}, false},
		{`doc.kind == "a" || doc.kind == "b" || "x" in doc.tags`, cel2filter.Or{Args: []cel2filter.Predicate{eq("kind", "a"), eq("kind", "b"), eq("tags", "x")}}, true},
	}
	for _, c := range cases {
		t.Run(c.expr, func(t *testing.T) {
			res := convert(t, env, c.expr)
			if !reflect.DeepEqual(res.Predicate, c.want) {
				t.Fatalf("predicate:\n got  %#v\n want %#v", res.Predicate, c.want)
			}
			require.Equal(t, c.exact, res.Exact, "exact")
		})
	}
}

func TestConvertOptions(t *testing.T) {
	env := newEnv(t)

	t.Run("field map", func(t *testing.T) {
		res := convert(t, env, `doc.meta.owner == "x" && doc.kind == "leaf"`, cel2filter.WithRoot("doc"), cel2filter.WithFieldMap(map[string]string{"meta.owner": "owner_email"}))
		want := cel2filter.And{Args: []cel2filter.Predicate{cel2filter.Eq{Field: "owner_email", Value: "x"}, cel2filter.Eq{Field: "kind", Value: "leaf"}}}
		require.Equal(t, want, res.Predicate)
		res = convert(t, env, `has(doc.meta.owner)`, cel2filter.WithRoot("doc"), cel2filter.WithFieldMap(map[string]string{"meta.owner": "owner_email"}))
		require.Equal(t, cel2filter.Exists{Field: "owner_email"}, res.Predicate)
	})

	t.Run("map field without encoder", func(t *testing.T) {
		res := convert(t, env, `doc.labels.Classification.Level == "x"`, cel2filter.WithRoot("doc"))
		require.Nil(t, res.Predicate)
		require.False(t, res.Exact)
		res = convert(t, env, `has(doc.labels)`, cel2filter.WithRoot("doc"))
		require.Equal(t, cel2filter.Exists{Field: "labels"}, res.Predicate)
	})

	t.Run("root inferred", func(t *testing.T) {
		res := convert(t, env, `doc.kind == "leaf" && doc.tags.exists(t, t == "x")`, cel2filter.WithEncoder("labels", flatkeys.Encoder{}))
		require.Equal(t, cel2filter.Eq{Field: "kind", Value: "leaf"}, res.Predicate)
		res = convert(t, env, `true`, cel2filter.WithEncoder("labels", flatkeys.Encoder{}))
		require.Nil(t, res.Predicate)
		require.True(t, res.Exact)
	})

	t.Run("root ambiguous", func(t *testing.T) {
		two := newEnv(t, cel.Variable("other", cel.StringType))
		_, err := cel2filter.Convert(compile(t, two, `doc.kind == other`))
		require.ErrorIs(t, err, cel2filter.ErrRootNotFound)
		var ce *cel2filter.ConversionError
		require.ErrorAs(t, err, &ce)
		require.Contains(t, ce.Internal(), "2 variables")
		res, err := cel2filter.Convert(compile(t, two, `doc.kind == "a" && other == "b"`), cel2filter.WithRoot("doc"))
		require.NoError(t, err)
		require.Equal(t, cel2filter.Eq{Field: "kind", Value: "a"}, res.Predicate)
		require.False(t, res.Exact)
	})

	t.Run("errors", func(t *testing.T) {
		_, err := cel2filter.Convert(nil)
		require.ErrorIs(t, err, cel2filter.ErrNilAST)
		parsed, issues := env.Parse(`doc.kind == "x"`)
		require.NoError(t, issues.Err())
		_, err = cel2filter.Convert(parsed, defaultOpts...)
		require.ErrorIs(t, err, cel2filter.ErrUncheckedAST)
		_, err = cel2filter.Convert(compile(t, env, `doc.kind`), defaultOpts...)
		require.ErrorIs(t, err, cel2filter.ErrNotBoolean)
		_, err = cel2filter.Convert(compile(t, env, `true`), cel2filter.WithMaxNodes(0))
		require.ErrorIs(t, err, cel2filter.ErrInvalidOption)
		_, err = cel2filter.Convert(compile(t, env, `true`), cel2filter.WithEncoder("labels", nil))
		require.ErrorIs(t, err, cel2filter.ErrInvalidOption)
	})

	t.Run("node cap", func(t *testing.T) {
		big := strings.Repeat(`doc.kind == "x" && `, 70) + "true"
		res := convert(t, env, big)
		require.Nil(t, res.Predicate)
		require.False(t, res.Exact)
		res = convert(t, env, big, cel2filter.WithRoot("doc"), cel2filter.WithMaxNodes(1000))
		require.IsType(t, cel2filter.And{}, res.Predicate)
		require.Len(t, res.Predicate.(cel2filter.And).Args, 70)
	})
}
