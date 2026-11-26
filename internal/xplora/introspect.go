package xplora

import (
	"context"
	"encoding/json"
	"fmt"
)

// GraphQL introspection, used by the `schema` command to discover how the
// auth mutations (refreshToken, signIn, ...) are defined, since they have
// never been captured from the official app.

// TypeRef is an introspected type reference (possibly wrapped in NON_NULL/LIST).
type TypeRef struct {
	Kind   string   `json:"kind"`
	Name   *string  `json:"name"`
	OfType *TypeRef `json:"ofType"`
}

// String renders the reference in GraphQL syntax, e.g. "[String!]!".
func (t *TypeRef) String() string {
	if t == nil {
		return "?"
	}
	switch t.Kind {
	case "NON_NULL":
		return t.OfType.String() + "!"
	case "LIST":
		return "[" + t.OfType.String() + "]"
	}
	if t.Name != nil {
		return *t.Name
	}
	return "?"
}

// BaseName returns the innermost named type.
func (t *TypeRef) BaseName() string {
	for t != nil {
		if t.Name != nil {
			return *t.Name
		}
		t = t.OfType
	}
	return ""
}

// InputValue is an argument or input field.
type InputValue struct {
	Name string  `json:"name"`
	Type TypeRef `json:"type"`
}

// Field is an object field (for Mutation/Query: an operation).
type Field struct {
	Name string       `json:"name"`
	Args []InputValue `json:"args"`
	Type TypeRef      `json:"type"`
}

// IntrospectedType is the result of an __type query.
type IntrospectedType struct {
	Name        string       `json:"name"`
	Kind        string       `json:"kind"`
	Fields      []Field      `json:"fields"`
	InputFields []InputValue `json:"inputFields"`
	EnumValues  []struct {
		Name string `json:"name"`
	} `json:"enumValues"`
}

// Signature renders a field as "name(arg: Type, ...): Type".
func (f Field) Signature() string {
	s := f.Name
	if len(f.Args) > 0 {
		s += "("
		for i, a := range f.Args {
			if i > 0 {
				s += ", "
			}
			s += a.Name + ": " + a.Type.String()
		}
		s += ")"
	}
	return s + ": " + f.Type.String()
}

const queryIntrospectRoots = `
query IntrospectRoots {
  __schema {
    queryType { name }
    mutationType { name }
  }
}`

const queryIntrospectType = `
query IntrospectType($name: String!) {
  __type(name: $name) {
    name
    kind
    fields(includeDeprecated: true) {
      name
      args { name type { ...TypeRef } }
      type { ...TypeRef }
    }
    inputFields { name type { ...TypeRef } }
    enumValues(includeDeprecated: true) { name }
  }
}
fragment TypeRef on __Type {
  kind name
  ofType { kind name ofType { kind name ofType { kind name ofType { kind name } } } }
}`

// IntrospectRoots returns the names of the query and mutation root types.
func (c *Client) IntrospectRoots(ctx context.Context) (query, mutation string, err error) {
	data, err := c.do(ctx, queryIntrospectRoots, nil)
	if err != nil {
		return "", "", err
	}
	var result struct {
		Schema struct {
			QueryType    *struct{ Name string } `json:"queryType"`
			MutationType *struct{ Name string } `json:"mutationType"`
		} `json:"__schema"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return "", "", fmt.Errorf("parsing __schema: %w", err)
	}
	if result.Schema.QueryType != nil {
		query = result.Schema.QueryType.Name
	}
	if result.Schema.MutationType != nil {
		mutation = result.Schema.MutationType.Name
	}
	return query, mutation, nil
}

// IntrospectType returns the definition of a named type, or nil if unknown.
func (c *Client) IntrospectType(ctx context.Context, name string) (*IntrospectedType, error) {
	data, err := c.do(ctx, queryIntrospectType, map[string]any{"name": name})
	if err != nil {
		return nil, err
	}
	var result struct {
		Type *IntrospectedType `json:"__type"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("parsing __type: %w", err)
	}
	return result.Type, nil
}
