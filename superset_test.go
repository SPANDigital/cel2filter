package cel2filter_test

import (
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	"cel.dev/cel-go/common/types"
	"github.com/stretchr/testify/require"

	"github.com/spandigital/cel2filter"
	"github.com/spandigital/cel2filter/dialect/googledrive"
	"github.com/spandigital/cel2filter/dialect/opensearch"
	"github.com/spandigital/cel2filter/dialect/s3vectors"
	"github.com/spandigital/cel2filter/flatkeys"
)

// The expressions the property test runs. They cover every translation rule, the constructs that do not
// push down, and the interplay of negation, absent fields and encoded maps.
var propertyExprs = []string{
	`"onboarding" in doc.tags`, `doc.kind == "leaf"`, `doc.kind != "leaf"`, `!(doc.kind == "leaf")`, `doc.kind == ""`,
	`doc.evergreen == true`, `doc.evergreen`, `!doc.evergreen`, `doc.evergreen == false`,
	`doc.category in ["policy", "People"]`, `!(doc.category in ["policy", "People"])`, `doc.category in ["", "policy"]`,
	`doc.path.startsWith("career")`, `!doc.path.startsWith("career")`, `has(doc.author)`, `!has(doc.author)`, `has(doc.tags)`,
	`has(doc.labels.Classification)`, `"Classification" in doc.labels`, `has(doc.labels.Classification.Level)`,
	`has(doc.labels.Classification) && doc.labels.Classification.Level == "Confidential"`,
	`has(doc.labels.Classification) && doc.labels.Classification.Level != "Confidential"`,
	`has(doc.labels.Classification) && "alice" in doc.labels.Classification.Owners`,
	`has(doc.labels.Classification) && doc.labels.Classification.Level in ["Public", "Internal"]`,
	`doc.labels[?"Classification"][?"Level"].orValue("") == "Confidential"`,
	`has(doc.labels.Classification) && doc.labels.Classification.Count == 3`,
	`doc.kind == "leaf" && "x" in doc.tags`, `doc.kind == "leaf" || "x" in doc.tags`,
	`doc.kind == "leaf" && doc.title.matches("x")`, `doc.kind == "leaf" || doc.title.matches("x")`,
	`!("x" in doc.tags)`, `doc.tags.exists(t, t == "x")`, `size(doc.tags) == 1`, `doc.title == doc.section`,
	`has(doc.labels)`, `size(doc.labels) > 0`, `size(doc.tags) != 0`, `size(doc.tags) > 1`, `2 <= size(doc.tags)`,
	`doc.kind == "leaf" && size(doc.labels) > 0`, `!(size(doc.tags) == 0)`,
	`doc.updated > timestamp("2024-06-01T00:00:00Z")`, `doc.updated >= timestamp("2024-06-01T00:00:00Z")`,
	`doc.updated < timestamp("2024-06-01T00:00:00Z")`, `doc.updated <= timestamp("2024-06-01T00:00:00.0005Z")`,
	`doc.updated > timestamp("2024-06-01T00:00:00.0005Z")`, `!(doc.updated > timestamp("2024-06-01T00:00:00Z"))`,
	`doc.source == "presidium" && doc.module.startsWith("span")`,
	`(doc.kind == "leaf" || doc.kind == "branch") && !(doc.category in ["policy"]) && "a" in doc.acl`,
	`doc.author in ["a", "b"] || (has(doc.labels.Classification) && doc.labels.Classification.Level == "Public")`,
	`doc.count > 3`, `doc.count >= 3`, `doc.count < 3`, `doc.count <= 3`, `doc.count > 2.5`, `doc.count < -1`, `3 < doc.count`,
	`doc.count != 3`, `doc.count == 3`, `doc.count == 0`, `!(doc.count > 3)`, `doc.count in [1, 3]`, `doc.count in [0, 3]`,
	`doc.score >= 3`, `doc.score < 2.5`, `doc.score > 1.0`, `doc.score > -1.5`, `doc.score != 2.5`,
	`doc.count > 3 || doc.score < 1.0`, `doc.kind == "a" && !(doc.count > 3 || doc.title.matches("x"))`,
	`!(doc.kind == "a" && "x" in doc.tags)`, `!(doc.kind == "a" || doc.count > 3)`,
	`doc.meta.owner == "o1" && doc.meta.n >= 2`, `has(doc.meta.owner)`, `!has(doc.meta)`,
}

func pick[T any](r *rand.Rand, opts ...T) T { return opts[r.IntN(len(opts))] }

func pickList(r *rand.Rand, opts ...string) []string {
	var out []string
	for _, o := range opts {
		if r.IntN(2) == 0 {
			out = append(out, o)
		}
	}
	return out
}

// genDoc builds a document with each field absent some of the time, so zero-value and presence semantics
// get exercised, and with values that collide with the literals in propertyExprs.
func genDoc(r *rand.Rand) cel2filter.MapFields {
	d := cel2filter.MapFields{}
	set := func(k string, v any) {
		if r.IntN(10) < 7 {
			d[k] = v
		}
	}
	set("source", pick(r, "presidium", "workspace"))
	set("module", pick(r, "span-handbook", "m"))
	set("key", "k")
	set("path", pick(r, "", "career", "career/x", "ops"))
	set("title", pick(r, "x", "y", ""))
	set("section", pick(r, "x", "s"))
	set("tags", pickList(r, "onboarding", "x"))
	set("acl", pickList(r, "a", "b"))
	set("author", pick(r, "", "a", "b"))
	set("category", pick(r, "", "policy", "People"))
	set("kind", pick(r, "", "leaf", "branch", "a"))
	set("evergreen", r.IntN(2) == 0)
	set("count", pick(r, int64(-3), 0, 1, 3, 4, 10))
	set("score", pick(r, -1.5, 0.0, 2.5, 3.0, 3.5))
	set("updated", pick(r,
		time.Date(2024, 5, 31, 0, 0, 0, 0, time.UTC), time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2024, 6, 1, 0, 0, 0, 500000, time.UTC), time.Date(2024, 6, 1, 0, 0, 0, int(time.Millisecond), time.UTC),
		time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)))
	if r.IntN(2) == 0 {
		meta := map[string]any{}
		if r.IntN(2) == 0 {
			meta["owner"] = pick(r, "o1", "o2", "")
		}
		if r.IntN(2) == 0 {
			meta["n"] = pick(r, int64(0), 2, 5)
		}
		d["meta"] = meta
	}
	if r.IntN(2) == 0 {
		labels := map[string]map[string]any{}
		if r.IntN(3) > 0 {
			fields := map[string]any{}
			if r.IntN(3) > 0 {
				fields["Level"] = pick(r, "Confidential", "Public", "Internal")
			}
			if r.IntN(2) == 0 {
				fields["Owners"] = pickList(r, "alice", "bob")
			}
			if r.IntN(2) == 0 {
				fields["Count"] = pick(r, "3", "4")
			}
			labels["Classification"] = fields
		}
		if r.IntN(3) == 0 {
			labels["Archive"] = map[string]any{}
		}
		d["labels"] = labels
	}
	return d
}

// store is the document as a store holds it: zero values omitted, nested objects flattened to dotted keys,
// the labels map flattened with flatkeys, timestamps as RFC 3339 strings when asString is set.
func store(d cel2filter.MapFields, asString bool) cel2filter.MapFields {
	out := cel2filter.MapFields{}
	for k, v := range d {
		switch v := v.(type) {
		case map[string]map[string]any:
			if flat := flatkeys.Flatten(v); len(flat) > 0 {
				out[k] = flat
			}
			if len(v) > 0 {
				out[k+"$present"] = true
			}
		case map[string]any:
			for kk, vv := range v {
				if !cel2filter.IsZero(vv) {
					out[k+"."+kk] = vv
				}
			}
			if len(v) > 0 {
				out[k] = v
			}
		case time.Time:
			if asString {
				out[k] = v.UTC().Format(time.RFC3339Nano)
			} else {
				out[k] = v
			}
		default:
			if !cel2filter.IsZero(v) {
				out[k] = v
			}
		}
	}
	return out
}

func TestSuperset(t *testing.T) {
	env := newEnv(t)
	r := rand.New(rand.NewPCG(1, 2)) //nolint:gosec // seeded, deterministic test data
	docs := make([]cel2filter.MapFields, 300)
	for i := range docs {
		docs[i] = genDoc(r)
	}
	for _, expr := range propertyExprs {
		t.Run(expr, func(t *testing.T) {
			ast := compile(t, env, expr)
			prg, err := env.Program(ast)
			require.NoError(t, err)
			res, err := cel2filter.Convert(ast, defaultOpts...)
			require.NoError(t, err)
			s3, _ := s3vectors.Render(res.Predicate)
			osq, osExact, _ := opensearch.Render(res.Predicate)
			drive := googledrive.Render(res.Predicate, driveOpts...)
			for _, d := range docs {
				out, _, err := prg.Eval(map[string]any{"doc": d})
				if err != nil || types.IsError(out) {
					continue
				}
				accepted := out.Value().(bool)
				st := store(d, false)
				matched := cel2filter.Match(res.Predicate, st)
				if accepted && !matched {
					t.Fatalf("expression accepts %v but predicate %v rejects it", d, res.Predicate)
				}
				if res.Exact && accepted != matched {
					t.Fatalf("exact predicate %v disagrees with the expression on %v: %v vs %v", res.Predicate, d, matched, accepted)
				}
				if s3.Filter != nil {
					if matched && !evalS3(s3.Filter, st) {
						t.Fatalf("S3 filter %v rejects %v accepted by %v", s3.Filter, st, res.Predicate)
					}
					if res.Exact && s3.Exact && evalS3(s3.Filter, st) != accepted {
						t.Fatalf("exact S3 filter %v disagrees on %v", s3.Filter, st)
					}
				}
				if drive.Query != "" {
					if matched && !evalDrive(drive.Query, d) {
						t.Fatalf("Drive query %q rejects %v accepted by %v", drive.Query, d, res.Predicate)
					}
					if res.Exact && drive.Exact && evalDrive(drive.Query, d) != accepted {
						t.Fatalf("exact Drive query %q disagrees on %v", drive.Query, d)
					}
				}
				if osq != nil {
					sts := store(d, true)
					if matched && !evalOS(osq, sts) {
						t.Fatalf("OpenSearch query %v rejects %v accepted by %v", osq, sts, res.Predicate)
					}
					if res.Exact && osExact && evalOS(osq, sts) != accepted {
						t.Fatalf("exact OpenSearch query %v disagrees on %v", osq, sts)
					}
				}
			}
		})
	}
}

// evalS3 interprets an S3 Vectors metadata filter the way the service documents it.
func evalS3(f map[string]any, meta cel2filter.MapFields) bool {
	for k, v := range f {
		switch k {
		case "$and":
			for _, sub := range v.([]any) {
				if !evalS3(sub.(map[string]any), meta) {
					return false
				}
			}
		case "$or":
			matchedAny := false
			for _, sub := range v.([]any) {
				if evalS3(sub.(map[string]any), meta) {
					matchedAny = true
				}
			}
			if !matchedAny {
				return false
			}
		default:
			stored, present := meta[k]
			for op, arg := range v.(map[string]any) {
				var ok bool
				switch op {
				case "$eq":
					ok = cel2filter.Match(cel2filter.Eq{Field: k, Value: arg}, meta)
				case "$ne":
					ok = present && !cel2filter.Match(cel2filter.Eq{Field: k, Value: arg}, meta)
				case "$in":
					ok = cel2filter.Match(cel2filter.In{Field: k, Values: arg.([]any)}, meta)
				case "$nin":
					ok = present && !cel2filter.Match(cel2filter.In{Field: k, Values: arg.([]any)}, meta)
				case "$startsWith":
					s, isStr := stored.(string)
					ok = isStr && strings.HasPrefix(s, arg.(string))
				case "$exists":
					ok = present == arg.(bool)
				case "$gt", "$gte", "$lt", "$lte":
					ops := map[string]cel2filter.CompareOp{"$gt": cel2filter.Gt, "$gte": cel2filter.Gte, "$lt": cel2filter.Lt, "$lte": cel2filter.Lte}
					_, isTime := stored.(time.Time)
					ok = !isTime && cel2filter.Match(cel2filter.Compare{Field: k, Op: ops[op], Value: arg}, meta)
				}
				if !ok {
					return false
				}
			}
		}
	}
	return true
}

// evalOS interprets the bool-filter subset of the Query DSL the dialect emits, over a document whose dates
// are RFC 3339 strings.
func evalOS(q map[string]any, src cel2filter.MapFields) bool {
	for kind, body := range q {
		switch kind {
		case "bool":
			b := body.(map[string]any)
			for _, sub := range listOf(b["filter"]) {
				if !evalOS(sub, src) {
					return false
				}
			}
			if should := listOf(b["should"]); len(should) > 0 {
				matchedAny := false
				for _, sub := range should {
					if evalOS(sub, src) {
						matchedAny = true
					}
				}
				if !matchedAny {
					return false
				}
			}
			if mn, ok := b["must_not"].(map[string]any); ok && evalOS(mn, src) {
				return false
			}
		case "term":
			for f, v := range body.(map[string]any) {
				if !cel2filter.Match(cel2filter.Eq{Field: f, Value: v}, src) {
					return false
				}
			}
		case "terms":
			for f, v := range body.(map[string]any) {
				if !cel2filter.Match(cel2filter.In{Field: f, Values: v.([]any)}, src) {
					return false
				}
			}
		case "prefix":
			for f, v := range body.(map[string]any) {
				if !cel2filter.Match(cel2filter.Prefix{Field: f, Value: v.(string)}, src) {
					return false
				}
			}
		case "exists":
			v, ok := src[body.(map[string]any)["field"].(string)]
			if !ok || cel2filter.IsZero(v) {
				return false
			}
		case "range":
			for f, bounds := range body.(map[string]any) {
				stored, ok := src[f]
				if !ok {
					return false
				}
				for op, bound := range bounds.(map[string]any) {
					ops := map[string]cel2filter.CompareOp{"gt": cel2filter.Gt, "gte": cel2filter.Gte, "lt": cel2filter.Lt, "lte": cel2filter.Lte}
					if s, isStr := bound.(string); isStr {
						bt, err := time.Parse(time.RFC3339Nano, s)
						st, err2 := time.Parse(time.RFC3339Nano, stored.(string))
						if err != nil || err2 != nil {
							return false
						}
						// a date field holds milliseconds
						if !cel2filter.Match(cel2filter.Compare{Field: f, Op: ops[op], Value: bt}, cel2filter.MapFields{f: st.Truncate(time.Millisecond)}) {
							return false
						}
						continue
					}
					if !cel2filter.Match(cel2filter.Compare{Field: f, Op: ops[op], Value: bound}, src) {
						return false
					}
				}
			}
		}
	}
	return true
}

func listOf(v any) []map[string]any {
	raw, _ := v.([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, e := range raw {
		out = append(out, e.(map[string]any))
	}
	return out
}
