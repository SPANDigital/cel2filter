package cel2filter_test

import (
	"encoding/json"
	"testing"

	"github.com/spandigital/cel2filter"
	"github.com/spandigital/cel2filter/dialect/opensearch"
	"github.com/spandigital/cel2filter/dialect/s3vectors"
)

// FuzzConvert checks that no expression the environment accepts makes Convert or a dialect panic, and that
// what the dialects render is JSON-serialisable.
func FuzzConvert(f *testing.F) {
	for _, expr := range propertyExprs {
		f.Add(expr)
	}
	f.Add(`doc.labels["A/B"]["C=D"] == "x/y=z"`)
	f.Add(`doc.count > -(5) && doc.updated == timestamp(0)`)
	env := newEnv(f)
	f.Fuzz(func(t *testing.T, expr string) {
		ast, issues := env.Compile(expr)
		if issues != nil && issues.Err() != nil {
			return
		}
		res, err := cel2filter.Convert(ast, defaultOpts...)
		if err != nil {
			return
		}
		if res.Predicate != nil {
			_ = res.Predicate.String()
		}
		s3, err := s3vectors.Render(res.Predicate, s3vectors.WithNegation(true), s3vectors.WithMaxConstraints(100))
		if err == nil {
			if _, err := json.Marshal(s3.Filter); err != nil {
				t.Fatalf("S3 filter does not marshal: %v", err)
			}
		}
		osq, _, err := opensearch.Render(res.Predicate)
		if err != nil {
			t.Fatalf("opensearch render: %v", err)
		}
		if _, err := json.Marshal(osq); err != nil {
			t.Fatalf("OpenSearch query does not marshal: %v", err)
		}
	})
}
