// Command basic declares a document, compiles a filter, translates it and renders it for both dialects.
package main

import (
	"encoding/json"
	"fmt"
	"log"

	"cel.dev/cel-go/cel"

	"github.com/spandigital/cel2filter"
	"github.com/spandigital/cel2filter/dialect/opensearch"
	"github.com/spandigital/cel2filter/dialect/s3vectors"
	"github.com/spandigital/cel2filter/flatkeys"
	"github.com/spandigital/cel2filter/schema"
)

func main() {
	doc := schema.NewSchema([]schema.FieldSchema{
		{Name: "kind", Type: cel.StringType},
		{Name: "title", Type: cel.StringType},
		{Name: "tags", Type: cel.StringType, Repeated: true},
		{Name: "count", Type: cel.IntType},
		{Name: "updated", Type: cel.TimestampType},
		{Name: "labels", Type: cel.MapType(cel.StringType, cel.MapType(cel.StringType, cel.DynType))},
	})
	env, err := cel.NewEnv(append(schema.EnvOptions("doc", "Doc", doc), cel.CrossTypeNumericComparisons(true))...)
	if err != nil {
		log.Fatal(err)
	}

	for _, expr := range []string{
		`"onboarding" in doc.tags && doc.kind == "leaf" && doc.updated > timestamp("2025-01-01T00:00:00Z")`,
		`has(doc.labels.Classification) && doc.labels.Classification.Level == "Public" && doc.count > 2.5`,
		`doc.kind == "leaf" || doc.title.matches("(?i)runbook")`,
		`!(doc.kind == "branch" || "internal" in doc.tags)`,
	} {
		ast, issues := env.Compile(expr)
		if issues != nil && issues.Err() != nil {
			log.Fatal(issues.Err())
		}
		res, err := cel2filter.Convert(ast, cel2filter.WithEncoder("labels", flatkeys.Encoder{}))
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("CEL:        %s\n", expr)
		fmt.Printf("predicate:  %v (exact=%v)\n", res.Predicate, res.Exact)

		s3, err := s3vectors.Render(res.Predicate, s3vectors.WithMaxConstraints(100))
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("s3vectors:  %s (exact=%v, constraints=%d)\n", js(s3.Filter), s3.Exact, s3.Constraints)

		q, exact, err := opensearch.Render(res.Predicate)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("opensearch: %s (exact=%v)\n\n", js(q), exact)
	}
}

func js(v map[string]any) string {
	if v == nil {
		return "(no constraint)"
	}
	b, _ := json.Marshal(v)
	return string(b)
}
