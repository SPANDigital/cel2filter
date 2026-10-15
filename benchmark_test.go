package cel2filter_test

import (
	"strings"
	"testing"

	"cel.dev/cel-go/cel"

	"github.com/spandigital/cel2filter"
	"github.com/spandigital/cel2filter/dialect/opensearch"
	"github.com/spandigital/cel2filter/dialect/s3vectors"
)

func benchmarkConvert(b *testing.B, expr string) {
	b.Helper()
	env := newEnv(b)
	ast := compile(b, env, expr)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := cel2filter.Convert(ast, defaultOpts...); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkConvertSimple(b *testing.B) {
	benchmarkConvert(b, `doc.kind == "leaf" && "x" in doc.tags`)
}

func BenchmarkConvertLabels(b *testing.B) {
	benchmarkConvert(b, `has(doc.labels.Classification) && doc.labels.Classification.Level in ["Public", "Internal"]`)
}

func BenchmarkConvertLarge(b *testing.B) {
	env := newEnv(b)
	ast := compile(b, env, strings.Repeat(`doc.kind == "x" && `, 30)+`doc.count > 3`)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := cel2filter.Convert(ast, cel2filter.WithRoot("doc"), cel2filter.WithMaxNodes(1000)); err != nil {
			b.Fatal(err)
		}
	}
}

func benchmarkPredicate(b *testing.B) cel2filter.Predicate {
	b.Helper()
	env := newEnv(b)
	res, err := cel2filter.Convert(compile(b, env, `doc.kind == "leaf" && ("x" in doc.tags || doc.category in ["a", "b"]) && doc.count > 3 && has(doc.author)`), defaultOpts...)
	if err != nil {
		b.Fatal(err)
	}
	return res.Predicate
}

func BenchmarkRenderS3Vectors(b *testing.B) {
	p := benchmarkPredicate(b)
	d := s3vectors.New(s3vectors.WithMaxConstraints(100))
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := d.RenderCounted(p); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRenderOpenSearch(b *testing.B) {
	p := benchmarkPredicate(b)
	d := opensearch.New()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, _, err := d.Render(p); err != nil {
			b.Fatal(err)
		}
	}
}

var _ = cel.BoolType
