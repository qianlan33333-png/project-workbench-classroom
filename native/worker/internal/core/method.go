package core

import (
	"encoding/json"
	"fmt"
	"github.com/go-openapi/jsonpointer"

	"github.com/google/jsonschema-go/jsonschema"
)

// A configured method can render an already resolved deterministic result.
// The fixed value is part of the same immutable input and output contract.
func (m Method) FixedOutput(records []Record) (string, bool, error) {
	if m.FixedOutputPointer == "" {
		return "", false, nil
	}
	p, err := jsonpointer.New(m.FixedOutputPointer)
	if err != nil || len(records) == 0 {
		return "", false, ErrInvalid
	}
	v, _, err := p.Get(records[0].Fields)
	if err != nil || v == nil {
		return "", false, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", false, ErrInvalid
	}
	if err = m.ValidateOutput(string(b)); err != nil {
		return "", false, err
	}
	return string(b), true, nil
}

// Select only a method already frozen by the caller. This never changes the
// candidate list and never asks the model to select a strategy or method.
func (r Run) MethodFor(records []Record) (Method, error) {
	if len(r.Methods) == 0 {
		return r.Method, nil
	}
	if len(records) == 0 {
		return Method{}, ErrInvalid
	}
	p, e := jsonpointer.New(r.MethodPointer)
	if e != nil {
		return Method{}, ErrInvalid
	}
	v, _, e := p.Get(records[0].Fields)
	if e != nil {
		return Method{}, ErrInvalid
	}
	key, ok := v.(string)
	if !ok {
		return Method{}, ErrInvalid
	}
	m, ok := r.Methods[key]
	if !ok || m.Instructions == "" {
		return Method{}, ErrInvalid
	}
	return m, nil
}

// Output contracts are standard JSON Schema. No business-specific parser lives here.
func (m Method) ResolveOutput() (*jsonschema.Resolved, error) {
	if len(m.OutputSchema) == 0 {
		return nil, nil
	}
	b, e := json.Marshal(m.OutputSchema)
	if e != nil || len(b) > 64000 {
		return nil, ErrInvalid
	}
	var schema jsonschema.Schema
	if e = json.Unmarshal(b, &schema); e != nil {
		return nil, fmt.Errorf("%w: invalid output schema", ErrInvalid)
	}
	resolved, e := schema.Resolve(nil) // Remote references are disabled by the SDK default.
	if e != nil {
		return nil, fmt.Errorf("%w: unresolved output schema", ErrInvalid)
	}
	return resolved, nil
}
func (m Method) ValidateOutput(text string) error {
	if text == "" || len(text) > 256*1024 {
		return ErrInvalid
	}
	schema, e := m.ResolveOutput()
	if e != nil {
		return e
	}
	if schema == nil {
		return nil
	}
	var value any
	if e = json.Unmarshal([]byte(text), &value); e != nil {
		return fmt.Errorf("model output is not valid JSON")
	}
	if e = schema.Validate(value); e != nil {
		return fmt.Errorf("model output does not satisfy the configured JSON Schema: %w", e)
	}
	return nil
}
