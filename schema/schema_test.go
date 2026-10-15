package schema_test

import (
	"testing"
	"time"

	"cel.dev/cel-go/cel"
	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/ext"
	"github.com/stretchr/testify/require"

	"github.com/spandigital/cel2filter"
	"github.com/spandigital/cel2filter/schema"
)

var s = schema.NewSchema([]schema.FieldSchema{
	{Name: "title", Type: cel.StringType},
	{Name: "count", Type: cel.IntType},
	{Name: "score", Type: cel.DoubleType},
	{Name: "ok", Type: cel.BoolType},
	{Name: "updated", Type: cel.TimestampType},
	{Name: "tags", Type: cel.StringType, Repeated: true},
	{Name: "labels", Type: cel.MapType(cel.StringType, cel.MapType(cel.StringType, cel.DynType))},
	{Name: "meta", Fields: []schema.FieldSchema{{Name: "owner", Type: cel.StringType}}},
	{Name: "anything"},
})

func newEnv(t *testing.T) *cel.Env {
	t.Helper()
	env, err := cel.NewEnv(append(schema.EnvOptions("doc", "Doc", s), cel.OptionalTypes(), ext.Strings(), cel.DefaultUTCTimeZone(true))...)
	require.NoError(t, err)
	return env
}

func eval(t *testing.T, env *cel.Env, expr string, doc any) any {
	t.Helper()
	ast, issues := env.Compile(expr)
	require.NoError(t, issues.Err(), expr)
	prg, err := env.Program(ast)
	require.NoError(t, err)
	out, _, err := prg.Eval(map[string]any{"doc": doc})
	require.NoError(t, err, expr)
	if types.IsError(out) {
		return out
	}
	return out.Value()
}

func TestSchema(t *testing.T) {
	f, ok := s.FindField("count")
	require.True(t, ok)
	require.Equal(t, cel.IntType, f.Type)
	_, ok = s.FindField("nope")
	require.False(t, ok)
	require.Len(t, s.Fields(), 9)
}

func TestEvaluation(t *testing.T) {
	env := newEnv(t)
	full := map[string]any{
		"title": "T", "count": int64(3), "score": 2.5, "ok": true, "updated": time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		"tags": []string{"a", "b"}, "labels": map[string]map[string]any{"L": {"F": "v", "M": []string{"x"}}},
		"meta": map[string]any{"owner": "o"}, "anything": int64(7),
	}
	empty := map[string]any{"title": "", "tags": []string{}}
	cases := []struct {
		expr string
		doc  any
		want any
	}{
		{`doc.title == "T" && doc.count == 3 && doc.score == 2.5 && doc.ok`, full, true},
		{`doc.updated > timestamp("2024-01-01T00:00:00Z")`, full, true},
		{`"b" in doc.tags && size(doc.tags) == 2`, full, true},
		{`doc.labels.L.F == "v" && "x" in doc.labels.L.M && has(doc.labels.L)`, full, true},
		{`doc.meta.owner == "o" && has(doc.meta.owner)`, full, true},
		{`doc.anything == 7`, full, true},
		{`doc.title.lowerAscii() == "t"`, full, true},
		{`doc.labels[?"L"][?"F"].orValue("") == "v"`, full, true},
		{`has(doc.title)`, empty, false},
		{`has(doc.tags)`, empty, false},
		{`has(doc.count)`, empty, false},
		{`has(doc.meta)`, empty, false},
		{`has(doc.meta.owner)`, empty, false},
		{`doc.title == "" && doc.count == 0 && doc.score == 0.0 && !doc.ok`, empty, true},
		{`doc.updated == timestamp("0001-01-01T00:00:00Z")`, empty, true},
		{`size(doc.tags) == 0 && size(doc.labels) == 0 && doc.meta.owner == ""`, empty, true},
		{`doc.labels[?"L"][?"F"].orValue("none") == "none"`, empty, true},
		{`doc.title == "T"`, cel2filter.MapFields{"title": "T"}, true},
		{`has(doc.title)`, cel2filter.MapFields{"title": "T"}, true},
	}
	for _, c := range cases {
		require.Equal(t, c.want, eval(t, env, c.expr, c.doc), c.expr)
	}
	// A missing label key is an error, as in CEL generally; a struct literal cannot be built from a schema.
	for _, expr := range []string{`doc.labels.L.F == "v"`, `Doc{title: "x"}.title == "x"`} {
		ast, issues := env.Compile(expr)
		require.NoError(t, issues.Err(), expr)
		prg, err := env.Program(ast)
		require.NoError(t, err)
		_, _, err = prg.Eval(map[string]any{"doc": empty})
		require.Error(t, err, expr)
	}
}

func TestTypeChecking(t *testing.T) {
	env := newEnv(t)
	for _, bad := range []string{`doc.nope == 1`, `doc.count == "x"`, `doc.tags == "x"`} {
		_, issues := env.Compile(bad)
		require.Error(t, issues.Err(), bad)
	}
	ast, issues := env.Compile(`doc.meta.owner`)
	require.NoError(t, issues.Err())
	require.True(t, ast.OutputType().IsExactType(cel.StringType))
	ast, issues = env.Compile(`doc.tags`)
	require.NoError(t, issues.Err())
	require.True(t, ast.OutputType().IsExactType(cel.ListType(cel.StringType)))
	_, err := cel.NewEnv(schema.Provider("Doc", s))
	require.NoError(t, err)
}
