package core

import (
	"fmt"
	"github.com/go-openapi/jsonpointer"
)

// A section may select subfields with standard RFC 6901 pointers. The pointer
// only sees that section's declared fields, never the whole stored record.
func validateSelection(s Section) error {
	if len(s.Select) > 128 {
		return ErrInvalid
	}
	for alias, path := range s.Select {
		if !ValidID(alias) {
			return ErrInvalid
		}
		if _, e := jsonpointer.New(path); e != nil {
			return fmt.Errorf("%w: invalid JSON Pointer", ErrInvalid)
		}
	}
	return nil
}
func projectFields(section Section, full map[string]any) (map[string]any, error) {
	allowed := map[string]any{}
	for _, f := range section.Fields {
		if v, ok := full[f]; ok {
			allowed[f] = v
		}
	}
	if len(section.Select) == 0 {
		return allowed, nil
	}
	out := map[string]any{}
	for alias, path := range section.Select {
		pointer, e := jsonpointer.New(path)
		if e != nil {
			return nil, ErrInvalid
		}
		value, _, e := pointer.Get(allowed)
		// Missing optional evidence stays explicitly null. Required fields are
		// checked by the fixed method before a model request can begin.
		if e != nil {
			out[alias] = nil
		} else {
			out[alias] = value
		}
	}
	return out, nil
}
