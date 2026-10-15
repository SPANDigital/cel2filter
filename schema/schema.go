// Package schema declares a document variable for CEL without a Go struct. A Schema lists the fields and
// their CEL types; Provider makes them visible to the type checker as an object type, and at evaluation
// time reads the fields from a map[string]any or from anything with a Field(name) (any, bool) method. An
// absent field reads as the zero value of its type and has() is false for it, the semantics native Go
// structs have and the semantics cel2filter's zero-value rules assume.
package schema

import (
	"errors"
	"fmt"
	"time"

	"cel.dev/cel-go/cel"
	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/common/types/ref"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// FieldSchema is one field. Fields makes it an object with sub-fields (Type is then ignored); Repeated wraps
// the type in a list.
type FieldSchema struct {
	Name     string
	Type     *cel.Type
	Repeated bool
	Fields   []FieldSchema
}

// Schema is a set of fields with O(1) lookup by name.
type Schema struct {
	fields []FieldSchema
	index  map[string]int
}

// NewSchema builds a Schema. A duplicate name keeps the last definition.
func NewSchema(fields []FieldSchema) Schema {
	s := Schema{fields: fields, index: make(map[string]int, len(fields))}
	for i, f := range fields {
		s.index[f.Name] = i
	}
	return s
}

// Fields returns the fields in declaration order.
func (s Schema) Fields() []FieldSchema { return s.fields }

// FindField looks a field up by name.
func (s Schema) FindField(name string) (*FieldSchema, bool) {
	i, ok := s.index[name]
	if !ok {
		return nil, false
	}
	return &s.fields[i], true
}

// ErrProvider reports an environment whose type provider cannot be wrapped.
var ErrProvider = errors.New("schema: the environment's type provider does not support registration")

// EnvOptions declares variable as an object of type typeName described by s. Put it after any other option
// that installs a type provider (ext.NativeTypes, cel.Types).
func EnvOptions(variable, typeName string, s Schema) []cel.EnvOption {
	return []cel.EnvOption{Provider(typeName, s), cel.Variable(variable, cel.ObjectType(typeName))}
}

// Provider wraps the environment's type provider so the checker sees typeName and its fields.
func Provider(typeName string, s Schema) cel.EnvOption {
	return func(e *cel.Env) (*cel.Env, error) {
		base, ok := e.CELTypeProvider().(registry)
		if !ok {
			return nil, fmt.Errorf("%w: %T", ErrProvider, e.CELTypeProvider())
		}
		p := &provider{registry: base, objects: map[string]Schema{}}
		p.add(typeName, s)
		return cel.CustomTypeProvider(p)(e)
	}
}

// registry is what cel.OptionalTypes and the extensions need from the provider they find in the environment.
type registry interface {
	types.Provider
	RegisterDescriptor(protoreflect.FileDescriptor) error
	RegisterType(...ref.Type) error
}

type provider struct {
	registry
	objects map[string]Schema
}

func (p *provider) add(typeName string, s Schema) {
	p.objects[typeName] = s
	for _, f := range s.fields {
		if len(f.Fields) > 0 {
			p.add(typeName+"."+f.Name, NewSchema(f.Fields))
		}
	}
}

func (p *provider) fieldType(typeName string, f *FieldSchema) *types.Type {
	var t *types.Type
	if len(f.Fields) > 0 {
		t = cel.ObjectType(typeName + "." + f.Name)
	} else {
		t = f.Type
		if t == nil {
			t = cel.DynType
		}
	}
	if f.Repeated {
		t = cel.ListType(t)
	}
	return t
}

func (p *provider) FindStructType(typeName string) (*types.Type, bool) {
	if _, ok := p.objects[typeName]; ok {
		return types.NewTypeTypeWithParam(types.NewObjectType(typeName)), true
	}
	return p.registry.FindStructType(typeName)
}

func (p *provider) FindStructFieldNames(typeName string) ([]string, bool) {
	s, ok := p.objects[typeName]
	if !ok {
		return p.registry.FindStructFieldNames(typeName)
	}
	names := make([]string, len(s.fields))
	for i, f := range s.fields {
		names[i] = f.Name
	}
	return names, true
}

func (p *provider) FindStructFieldType(typeName, fieldName string) (*types.FieldType, bool) {
	s, ok := p.objects[typeName]
	if !ok {
		return p.registry.FindStructFieldType(typeName, fieldName)
	}
	f, ok := s.FindField(fieldName)
	if !ok {
		return nil, false
	}
	t := p.fieldType(typeName, f)
	return &types.FieldType{
		Type: t,
		IsSet: func(obj any) bool {
			v, ok := lookup(obj, fieldName)
			return ok && !isZero(v)
		},
		GetFrom: func(obj any) (any, error) {
			v, ok := lookup(obj, fieldName)
			if !ok || v == nil {
				return zeroOf(t), nil
			}
			return v, nil
		},
	}, true
}

func (p *provider) NewValue(typeName string, _ map[string]ref.Val) ref.Val {
	if _, ok := p.objects[typeName]; ok {
		return types.NewErr("schema: %s cannot be constructed in an expression", typeName)
	}
	return p.registry.NewValue(typeName, nil)
}

type fields interface{ Field(name string) (any, bool) }

func lookup(obj any, name string) (any, bool) {
	switch o := obj.(type) {
	case map[string]any:
		v, ok := o[name]
		return v, ok
	case fields:
		return o.Field(name)
	case ref.Val:
		return lookup(o.Value(), name)
	}
	return nil, false
}

func isZero(v any) bool {
	switch v := v.(type) {
	case nil:
		return true
	case string:
		return v == ""
	case bool:
		return !v
	case int:
		return v == 0
	case int64:
		return v == 0
	case float64:
		return v == 0
	case time.Time:
		return v.IsZero()
	case []string:
		return len(v) == 0
	case []any:
		return len(v) == 0
	case []int64:
		return len(v) == 0
	case []float64:
		return len(v) == 0
	case map[string]any:
		return len(v) == 0
	case map[string]map[string]any:
		return len(v) == 0
	}
	return false
}

func zeroOf(t *types.Type) any {
	switch t.Kind() {
	case types.StringKind:
		return ""
	case types.BoolKind:
		return false
	case types.IntKind:
		return int64(0)
	case types.UintKind:
		return uint64(0)
	case types.DoubleKind:
		return float64(0)
	case types.TimestampKind:
		return time.Time{}
	case types.ListKind:
		return []any{}
	case types.MapKind, types.StructKind:
		return map[string]any{}
	}
	return nil
}
