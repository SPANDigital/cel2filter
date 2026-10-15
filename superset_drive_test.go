package cel2filter_test

import (
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/spandigital/cel2filter"
	"github.com/spandigital/cel2filter/dialect/googledrive"
)

// driveLabels resolves the property documents' labels with the label title as its id.
type driveLabels struct{}

func (driveLabels) Any() (string, []string, bool, bool) {
	return "('labels/Classification' in labels) or ('labels/Archive' in labels)", []string{"Archive", "Classification"}, true, true
}

func (driveLabels) Entry(label, field string, value *string) (string, []string, bool, bool) {
	if label != "Classification" && label != "Archive" {
		return "", nil, false, false
	}
	switch {
	case field == "":
		return fmt.Sprintf("'labels/%s' in labels", label), []string{label}, true, true
	case value == nil:
		return fmt.Sprintf("labels/%s.%s is not null", label, field), []string{label}, true, true
	}
	return fmt.Sprintf("labels/%s.%s = %s", label, field, googledrive.Quote(*value)), []string{label}, false, true
}

var driveOpts = []googledrive.Option{
	googledrive.WithTerm("title", "name", googledrive.String),
	googledrive.WithTerm("kind", "mimeType", googledrive.String),
	googledrive.WithTerm("updated", "modifiedTime", googledrive.Time),
	googledrive.WithLabels("labels", driveLabels{}),
}

// evalDrive interprets the subset of the Drive query language the dialect emits, with Drive's semantics:
// name contains matches a prefix of any word of the name.
func evalDrive(q string, d cel2filter.MapFields) bool {
	p := &driveParser{toks: driveTokens(q)}
	v := p.or(d)
	if p.pos != len(p.toks) {
		panic(fmt.Sprintf("trailing tokens in %q at %d", q, p.pos))
	}
	return v
}

type driveParser struct {
	toks []string
	pos  int
}

func (p *driveParser) peek() string {
	if p.pos < len(p.toks) {
		return p.toks[p.pos]
	}
	return ""
}
func (p *driveParser) next() string { t := p.peek(); p.pos++; return t }

func (p *driveParser) or(d cel2filter.MapFields) bool {
	v := p.and(d)
	for p.peek() == "or" {
		p.next()
		w := p.and(d)
		v = v || w
	}
	return v
}

func (p *driveParser) and(d cel2filter.MapFields) bool {
	v := p.unary(d)
	for p.peek() == "and" {
		p.next()
		w := p.unary(d)
		v = v && w
	}
	return v
}

func (p *driveParser) unary(d cel2filter.MapFields) bool {
	switch p.peek() {
	case "not":
		p.next()
		return !p.unary(d)
	case "(":
		p.next()
		v := p.or(d)
		if p.next() != ")" {
			panic("missing )")
		}
		return v
	}
	return p.term(d)
}

func (p *driveParser) term(d cel2filter.MapFields) bool {
	first := p.next()
	if strings.HasPrefix(first, "'") { // 'labels/T' in labels
		if p.next() != "in" || p.next() != "labels" {
			panic("bad in term")
		}
		labels, _ := d["labels"].(map[string]map[string]any)
		_, ok := labels[strings.TrimPrefix(unquote(first), "labels/")]
		return ok
	}
	op := p.next()
	if op == "is" {
		p.next() // not
		p.next() // null
		v, _ := labelValue(d, first)
		return v != nil && !cel2filter.IsZero(v)
	}
	arg := unquote(p.next())
	switch {
	case strings.HasPrefix(first, "labels/"):
		v, _ := labelValue(d, first)
		switch v := v.(type) {
		case string:
			return v == arg
		case []string:
			for _, e := range v {
				if e == arg {
					return true
				}
			}
		}
		return false
	case first == "modifiedTime":
		t, _ := d["updated"].(time.Time)
		at, err := time.Parse(time.RFC3339, arg)
		if err != nil {
			panic(err)
		}
		switch op {
		case "=":
			return t.Equal(at)
		case "!=":
			return !t.Equal(at)
		case ">":
			return t.After(at)
		case ">=":
			return !t.Before(at)
		case "<":
			return t.Before(at)
		case "<=":
			return !t.After(at)
		}
	default:
		field := map[string]string{"name": "title", "mimeType": "kind"}[first]
		s, _ := d[field].(string)
		switch op {
		case "=":
			return s == arg
		case "!=":
			return s != arg
		case "contains":
			if first == "mimeType" {
				return strings.Contains(s, arg)
			}
			for _, w := range strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
				if strings.HasPrefix(w, arg) {
					return true
				}
			}
			return strings.HasPrefix(s, arg)
		}
	}
	panic("unknown term " + first + " " + op)
}

func labelValue(d cel2filter.MapFields, ref string) (any, bool) {
	title, field, _ := strings.Cut(strings.TrimPrefix(ref, "labels/"), ".")
	labels, _ := d["labels"].(map[string]map[string]any)
	v, ok := labels[title][field]
	return v, ok
}

func unquote(s string) string {
	s = strings.TrimSuffix(strings.TrimPrefix(s, "'"), "'")
	return strings.NewReplacer(`\'`, `'`, `\\`, `\`).Replace(s)
}

func driveTokens(q string) []string {
	var toks []string
	for i := 0; i < len(q); {
		switch c := q[i]; c {
		case ' ':
			i++
		case '(', ')':
			toks = append(toks, string(c))
			i++
		case '\'':
			j := i + 1
			for ; j < len(q) && q[j] != '\''; j++ {
				if q[j] == '\\' {
					j++
				}
			}
			toks = append(toks, q[i:j+1])
			i = j + 1
		default:
			j := i
			for j < len(q) && q[j] != ' ' && q[j] != '(' && q[j] != ')' {
				j++
			}
			toks = append(toks, q[i:j])
			i = j
		}
	}
	return toks
}
