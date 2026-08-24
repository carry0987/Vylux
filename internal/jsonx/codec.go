package jsonx

import (
	"bytes"
	"encoding/json"
)

// Codec namespaced generic JSON conversion helpers.
// Go 1.27 generic methods let us keep these helpers scoped to one type
// instead of duplicating free generic functions across packages.
type Codec struct{}

// DecodeStrict decodes a map into T while rejecting unknown fields.
func (Codec) DecodeStrict[T any](opts map[string]any) (T, error) {
	var decoded T
	if opts == nil {
		return decoded, nil
	}
	data, err := json.Marshal(opts)
	if err != nil {
		return decoded, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&decoded); err != nil {
		return decoded, err
	}
	return decoded, nil
}

// ToMap converts a typed options struct into its canonical map form.
func (Codec) ToMap[T any](opts T) (map[string]any, error) {
	data, err := json.Marshal(opts)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = map[string]any{}
	}
	return out, nil
}

var StrictCodec Codec
