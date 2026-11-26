package xplora

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

type introspectRT struct{ ops []string }

func (r *introspectRT) RoundTrip(req *http.Request) (*http.Response, error) {
	var body struct {
		OperationName string         `json:"operationName"`
		Variables     map[string]any `json:"variables"`
	}
	raw, _ := io.ReadAll(req.Body)
	_ = json.Unmarshal(raw, &body)
	r.ops = append(r.ops, body.OperationName)
	var out string
	switch body.OperationName {
	case "IntrospectRoots":
		out = `{"data":{"__schema":{"queryType":{"name":"Query"},"mutationType":{"name":"Mutation"}}}}`
	case "IntrospectType":
		out = `{"data":{"__type":{"name":"Mutation","kind":"OBJECT","fields":[
			{"name":"refreshToken","args":[
				{"name":"uid","type":{"kind":"NON_NULL","name":null,"ofType":{"kind":"SCALAR","name":"String","ofType":null}}},
				{"name":"clientId","type":{"kind":"SCALAR","name":"String","ofType":null}}],
			 "type":{"kind":"OBJECT","name":"Token","ofType":null}},
			{"name":"tags","args":[],"type":{"kind":"NON_NULL","name":null,"ofType":{"kind":"LIST","name":null,"ofType":{"kind":"NON_NULL","name":null,"ofType":{"kind":"SCALAR","name":"String","ofType":null}}}}}
		]}}}`
	}
	return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(bytes.NewBufferString(out))}, nil
}

func TestIntrospection(t *testing.T) {
	rt := &introspectRT{}
	c := NewClient(NewAuth(t.TempDir()), WithHTTPClient(&http.Client{Transport: rt}))

	q, m, err := c.IntrospectRoots(t.Context())
	if err != nil || q != "Query" || m != "Mutation" {
		t.Fatalf("roots = %q, %q, %v", q, m, err)
	}
	typ, err := c.IntrospectType(t.Context(), "Mutation")
	if err != nil || typ == nil || len(typ.Fields) != 2 {
		t.Fatalf("type = %+v, %v", typ, err)
	}
	if got, want := typ.Fields[0].Signature(), "refreshToken(uid: String!, clientId: String): Token"; got != want {
		t.Errorf("signature = %q, want %q", got, want)
	}
	if got, want := typ.Fields[1].Signature(), "tags: [String!]!"; got != want {
		t.Errorf("signature = %q, want %q", got, want)
	}
	if got := typ.Fields[1].Type.BaseName(); got != "String" {
		t.Errorf("base name = %q, want String", got)
	}
	if rt.ops[0] != "IntrospectRoots" || rt.ops[1] != "IntrospectType" {
		t.Errorf("operation names sent = %v", rt.ops)
	}
}
