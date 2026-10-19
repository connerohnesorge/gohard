package gen

import (
	"encoding/json"
	"fmt"
)

// Model is the part of the LSP metaModel.json schema the generator
// reads.
type Model struct {
	MetaData struct {
		Version string `json:"version"`
	} `json:"metaData"`
	Requests      []Call        `json:"requests"`
	Notifications []Call        `json:"notifications"`
	Structures    []Structure   `json:"structures"`
	Enumerations  []Enumeration `json:"enumerations"`
	TypeAliases   []TypeAlias   `json:"typeAliases"`
}

// Call is a request or a notification.
type Call struct {
	Method              string          `json:"method"`
	Params              json.RawMessage `json:"params"`
	Result              *Type           `json:"result"`
	PartialResult       *Type           `json:"partialResult"`
	RegistrationOptions *Type           `json:"registrationOptions"`
	MessageDirection    string          `json:"messageDirection"`
	Documentation       string          `json:"documentation"`
	Proposed            bool            `json:"proposed"`
}

// params returns the parameter types: none, one, or (in the metaModel
// schema, never used by 3.17) several.
func (c *Call) params() ([]*Type, error) {
	if len(c.Params) == 0 {
		return nil, nil
	}
	if c.Params[0] == '[' {
		var ts []*Type
		err := json.Unmarshal(c.Params, &ts)
		return ts, err
	}
	var t Type
	if err := json.Unmarshal(c.Params, &t); err != nil {
		return nil, err
	}
	return []*Type{&t}, nil
}

// Structure is a named structure.
type Structure struct {
	Name          string     `json:"name"`
	Extends       []*Type    `json:"extends"`
	Mixins        []*Type    `json:"mixins"`
	Properties    []Property `json:"properties"`
	Documentation string     `json:"documentation"`
	Deprecated    string     `json:"deprecated"`
	Proposed      bool       `json:"proposed"`
}

// Property is a structure or literal property.
type Property struct {
	Name          string `json:"name"`
	Type          *Type  `json:"type"`
	Optional      bool   `json:"optional"`
	Documentation string `json:"documentation"`
	Deprecated    string `json:"deprecated"`
	Proposed      bool   `json:"proposed"`
}

// Enumeration is a named enumeration.
type Enumeration struct {
	Name          string      `json:"name"`
	Type          *Type       `json:"type"`
	Values        []EnumValue `json:"values"`
	Documentation string      `json:"documentation"`
	Deprecated    string      `json:"deprecated"`
	Proposed      bool        `json:"proposed"`
}

// EnumValue is one enumeration member; Value is a string or a number.
type EnumValue struct {
	Name          string          `json:"name"`
	Value         json.RawMessage `json:"value"`
	Documentation string          `json:"documentation"`
	Deprecated    string          `json:"deprecated"`
	Proposed      bool            `json:"proposed"`
}

// TypeAlias is a named type alias.
type TypeAlias struct {
	Name          string `json:"name"`
	Type          *Type  `json:"type"`
	Documentation string `json:"documentation"`
	Deprecated    string `json:"deprecated"`
	Proposed      bool   `json:"proposed"`
}

// Type is a metaModel type expression. Value depends on Kind: a Type
// for "map", a literal structure for "literal", a string for
// "stringLiteral".
type Type struct {
	Kind    string          `json:"kind"`
	Name    string          `json:"name"`
	Element *Type           `json:"element"`
	Key     *Type           `json:"key"`
	Items   []*Type         `json:"items"`
	Value   json.RawMessage `json:"value"`
}

// literal decodes the properties of a "literal" type.
func (t *Type) literal() ([]Property, error) {
	var v struct {
		Properties []Property `json:"properties"`
	}
	if err := json.Unmarshal(t.Value, &v); err != nil {
		return nil, fmt.Errorf("literal type: %w", err)
	}
	return v.Properties, nil
}

// mapValue decodes the value type of a "map" type.
func (t *Type) mapValue() (*Type, error) {
	var v Type
	if err := json.Unmarshal(t.Value, &v); err != nil {
		return nil, fmt.Errorf("map value type: %w", err)
	}
	return &v, nil
}
