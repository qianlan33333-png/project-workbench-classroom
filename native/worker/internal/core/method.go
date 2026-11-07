package core

import (
	"encoding/json"
	"fmt"

	"github.com/google/jsonschema-go/jsonschema"
)

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
