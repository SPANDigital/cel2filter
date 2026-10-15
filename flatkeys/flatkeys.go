// Package flatkeys stores a two-level map of strings (a label title to field names to values, say) as a flat
// list of strings, so a lookup into it pushes down as a plain equality on a keyword field:
//
//	"Classification"                       the first-level key is present
//	"Classification/Level"                 the second-level key has a value
//	"Classification/Level/"                that value is a list
//	"Classification/Level=Confidential"    one value (every element of a list gets its own entry)
//
// Keys escape "\", "/" and "=" with a backslash; a value is everything after the first unescaped "=" and
// needs no escaping. Encoder implements cel2filter.Encoder for a field stored this way, so that
// has(doc.labels.Classification) becomes labels == "Classification" and
// doc.labels.Classification.Level == "Confidential" becomes labels == "Classification/Level=Confidential".
package flatkeys

import (
	"sort"
	"strconv"
	"strings"
	"time"
)

// Encoder encodes lookups for a field stored with Flatten.
type Encoder struct{}

// Encode implements cel2filter.Encoder: one or two keys for a presence test, exactly two keys with a value.
func (Encoder) Encode(keys []string, value any) (string, bool) {
	switch {
	case value == nil && (len(keys) == 1 || len(keys) == 2):
		return Entry(keys[0], at(keys, 1), nil), true
	case value != nil && len(keys) == 2:
		s, ok := canon(value)
		if !ok {
			return "", false
		}
		return Entry(keys[0], keys[1], &s), true
	}
	return "", false
}

func at(keys []string, i int) string {
	if i < len(keys) {
		return keys[i]
	}
	return ""
}

// Flatten renders a two-level map as its stored list, sorted. Values are strings or lists of strings
// (or any scalar Canon understands).
func Flatten(m map[string]map[string]any) []string {
	if len(m) == 0 {
		return nil
	}
	var out []string
	for key, fields := range m {
		out = append(out, escape(key))
		for field, v := range fields {
			prefix := escape(key) + "/" + escape(field)
			out = append(out, prefix)
			switch v := v.(type) {
			case []string:
				out = append(out, prefix+"/")
				for _, s := range v {
					out = append(out, prefix+"="+s)
				}
			case []any:
				out = append(out, prefix+"/")
				for _, e := range v {
					out = append(out, prefix+"="+Canon(e))
				}
			default:
				out = append(out, prefix+"="+Canon(v))
			}
		}
	}
	sort.Strings(out)
	return out
}

// Unflatten rebuilds the map from its stored list.
func Unflatten(flat []string) map[string]map[string]any {
	if len(flat) == 0 {
		return nil
	}
	type fieldState struct {
		multi  bool
		values []string
	}
	keys := map[string]map[string]*fieldState{}
	for _, entry := range flat {
		name, value, hasValue := splitValue(entry)
		parts := splitName(name)
		if len(parts) == 0 || parts[0] == "" {
			continue
		}
		fields, ok := keys[parts[0]]
		if !ok {
			fields = map[string]*fieldState{}
			keys[parts[0]] = fields
		}
		if len(parts) < 2 || parts[1] == "" {
			continue
		}
		st, ok := fields[parts[1]]
		if !ok {
			st = &fieldState{}
			fields[parts[1]] = st
		}
		switch {
		case hasValue:
			st.values = append(st.values, value)
		case len(parts) == 3 && parts[2] == "":
			st.multi = true
		}
	}
	out := make(map[string]map[string]any, len(keys))
	for key, fields := range keys {
		m := map[string]any{}
		for field, st := range fields {
			switch {
			case st.multi:
				m[field] = append([]string{}, st.values...)
			case len(st.values) == 1:
				m[field] = st.values[0]
			case len(st.values) > 1:
				m[field] = append([]string{}, st.values...)
			}
		}
		out[key] = m
	}
	return out
}

// Entry renders the stored form of a lookup: the first-level key alone, a field, or a field with one value.
func Entry(key, field string, value *string) string {
	s := escape(key)
	if field != "" {
		s += "/" + escape(field)
	}
	if value != nil {
		s += "=" + *value
	}
	return s
}

// Parse is the inverse of Entry: the first-level key, the field ("" for a key alone) and the value (nil for
// a presence entry). ok is false for the list marker ("Key/Field/") and anything malformed.
func Parse(entry string) (key, field string, value *string, ok bool) {
	name, v, hasValue := splitValue(entry)
	parts := splitName(name)
	switch {
	case len(parts) == 0 || parts[0] == "" || len(parts) > 2:
		return "", "", nil, false
	case len(parts) == 2 && parts[1] == "":
		return "", "", nil, false
	case hasValue && len(parts) != 2:
		return "", "", nil, false
	}
	key = parts[0]
	if len(parts) == 2 {
		field = parts[1]
	}
	if hasValue {
		value = &v
	}
	return key, field, value, true
}

// Canon renders a scalar the way values are stored and compared.
func Canon(v any) string {
	s, _ := canon(v)
	return s
}

func canon(v any) (string, bool) {
	switch v := v.(type) {
	case string:
		return v, true
	case bool:
		return strconv.FormatBool(v), true
	case int:
		return strconv.Itoa(v), true
	case int64:
		return strconv.FormatInt(v, 10), true
	case float64:
		if v == float64(int64(v)) {
			return strconv.FormatInt(int64(v), 10), true
		}
		return strconv.FormatFloat(v, 'f', -1, 64), true
	case time.Time:
		return v.UTC().Format(time.RFC3339), true
	}
	return "", false
}

func escape(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r == '\\' || r == '/' || r == '=' {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// splitValue cuts an entry at the first unescaped "=".
func splitValue(entry string) (name, value string, ok bool) {
	for i := 0; i < len(entry); i++ {
		switch entry[i] {
		case '\\':
			i++
		case '=':
			return entry[:i], entry[i+1:], true
		}
	}
	return entry, "", false
}

// splitName splits a name at unescaped "/" and unescapes the pieces.
func splitName(name string) []string {
	var parts []string
	var b strings.Builder
	for i := 0; i < len(name); i++ {
		switch c := name[i]; c {
		case '\\':
			if i+1 < len(name) {
				i++
				b.WriteByte(name[i])
			}
		case '/':
			parts = append(parts, b.String())
			b.Reset()
		default:
			b.WriteByte(c)
		}
	}
	return append(parts, b.String())
}
