package llm

import (
	"encoding/json"
	"slices"
)

// SchemaType identifies a JSON Schema primitive or compound value type.
type SchemaType string

// Supported JSON Schema type identifiers.
const (
	// TypeObject represents a JSON object node.
	TypeObject SchemaType = "object"
	// TypeArray represents a JSON array node.
	TypeArray SchemaType = "array"
	// TypeString represents a JSON string node.
	TypeString SchemaType = "string"
	// TypeNumber represents a JSON floating-point or numeric node.
	TypeNumber SchemaType = "number"
	// TypeInteger represents a JSON integer node.
	TypeInteger SchemaType = "integer"
	// TypeBoolean represents a JSON boolean node.
	TypeBoolean SchemaType = "boolean"
	// TypeNull represents a JSON null node.
	TypeNull SchemaType = "null"
)

// SchemaTypes encodes one or more JSON Schema types; a single element marshals as a string,
// while multiple elements marshal as a JSON array (e.g. ["string", "null"]).
type SchemaTypes []SchemaType

// Contains reports whether target is included in st.
func (st SchemaTypes) Contains(target SchemaType) bool {
	return slices.Contains(st, target)
}

// MarshalJSON formats a single type as a JSON string and multiple types as a JSON array.
func (st SchemaTypes) MarshalJSON() ([]byte, error) {
	if len(st) == 0 {
		return []byte("null"), nil
	}
	if len(st) == 1 {
		return json.Marshal(string(st[0]))
	}
	return json.Marshal([]SchemaType(st))
}

// UnmarshalJSON decodes either a single JSON type string or an array of type strings.
func (st *SchemaTypes) UnmarshalJSON(data []byte) error {
	var single SchemaType
	if err := json.Unmarshal(data, &single); err == nil {
		if single == "" {
			*st = nil
		} else {
			*st = SchemaTypes{single}
		}
		return nil
	}
	var multi []SchemaType
	if err := json.Unmarshal(data, &multi); err != nil {
		return err
	}
	*st = SchemaTypes(multi)
	return nil
}

// Schema defines a strongly typed JSON Schema node free of untyped map[string]any containers.
type Schema struct {
	Type                 SchemaTypes        `json:"type,omitempty"`
	Description          string             `json:"description,omitempty"`
	Properties           map[string]*Schema `json:"properties,omitempty"`
	Items                *Schema            `json:"items,omitempty"`
	Required             []string           `json:"-"`
	AdditionalProperties *bool              `json:"additionalProperties,omitempty"`
	Enum                 []string           `json:"enum,omitempty"`
	AnyOf                []*Schema          `json:"anyOf,omitempty"`
	OneOf                []*Schema          `json:"oneOf,omitempty"`
	AllOf                []*Schema          `json:"allOf,omitempty"`
	PrefixItems          []*Schema          `json:"prefixItems,omitempty"`
}

// schemaJSONWire mirrors Schema for encoding/decoding while preserving non-nil empty Required slices.
type schemaJSONWire struct {
	Type                 SchemaTypes        `json:"type,omitempty"`
	Description          string             `json:"description,omitempty"`
	Properties           map[string]*Schema `json:"properties,omitempty"`
	Items                *Schema            `json:"items,omitempty"`
	Required             *[]string          `json:"required,omitempty"`
	AdditionalProperties *bool              `json:"additionalProperties,omitempty"`
	Enum                 []string           `json:"enum,omitempty"`
	AnyOf                []*Schema          `json:"anyOf,omitempty"`
	OneOf                []*Schema          `json:"oneOf,omitempty"`
	AllOf                []*Schema          `json:"allOf,omitempty"`
	PrefixItems          []*Schema          `json:"prefixItems,omitempty"`
}

// MarshalJSON encodes Schema while emitting explicit non-nil empty Required slices as "required": [].
func (s Schema) MarshalJSON() ([]byte, error) {
	wire := schemaJSONWire{
		Type:                 s.Type,
		Description:          s.Description,
		Properties:           s.Properties,
		Items:                s.Items,
		AdditionalProperties: s.AdditionalProperties,
		Enum:                 s.Enum,
		AnyOf:                s.AnyOf,
		OneOf:                s.OneOf,
		AllOf:                s.AllOf,
		PrefixItems:          s.PrefixItems,
	}
	if s.Required != nil {
		req := s.Required
		wire.Required = &req
	}
	return json.Marshal(wire)
}

// UnmarshalJSON decodes Schema while preserving explicit empty "required": [] slices.
func (s *Schema) UnmarshalJSON(data []byte) error {
	var wire schemaJSONWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	s.Type = wire.Type
	s.Description = wire.Description
	s.Properties = wire.Properties
	s.Items = wire.Items
	s.AdditionalProperties = wire.AdditionalProperties
	s.Enum = wire.Enum
	s.AnyOf = wire.AnyOf
	s.OneOf = wire.OneOf
	s.AllOf = wire.AllOf
	s.PrefixItems = wire.PrefixItems
	if wire.Required != nil {
		s.Required = *wire.Required
	}
	return nil
}

// JSON returns the indented JSON representation of s.
func (s *Schema) JSON() (string, error) {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// Clone returns an independent deep copy of s so transport-specific mutations never alias shared schemas.
func (s *Schema) Clone() *Schema {
	if s == nil {
		return nil
	}
	out := &Schema{
		Type:        slices.Clone(s.Type),
		Description: s.Description,
		Items:       s.Items.Clone(),
		Enum:        slices.Clone(s.Enum),
		AnyOf:       cloneSchemaSlice(s.AnyOf),
		OneOf:       cloneSchemaSlice(s.OneOf),
		AllOf:       cloneSchemaSlice(s.AllOf),
		PrefixItems: cloneSchemaSlice(s.PrefixItems),
	}
	if s.Required != nil {
		out.Required = slices.Clone(s.Required)
	}
	if s.AdditionalProperties != nil {
		ap := *s.AdditionalProperties
		out.AdditionalProperties = &ap
	}
	if s.Properties != nil {
		out.Properties = make(map[string]*Schema, len(s.Properties))
		for k, v := range s.Properties {
			out.Properties[k] = v.Clone()
		}
	}
	return out
}

// cloneSchemaSlice deep-clones a slice of child Schema pointers while preserving nil slices.
func cloneSchemaSlice(items []*Schema) []*Schema {
	if items == nil {
		return nil
	}
	out := make([]*Schema, len(items))
	for i, item := range items {
		out[i] = item.Clone()
	}
	return out
}

// Strict returns a deep copy of s normalized for OpenAI and Anthropic strict structured output modes:
// every object sets additionalProperties=false, requires every property in sorted order, and makes
// previously optional properties accept null.
func (s *Schema) Strict() *Schema {
	if s == nil {
		return nil
	}
	out := &Schema{
		Type:        slices.Clone(s.Type),
		Description: s.Description,
		Items:       s.Items.Strict(),
		Enum:        slices.Clone(s.Enum),
		AnyOf:       strictSchemaSlice(s.AnyOf),
		OneOf:       strictSchemaSlice(s.OneOf),
		AllOf:       strictSchemaSlice(s.AllOf),
		PrefixItems: strictSchemaSlice(s.PrefixItems),
	}
	if s.Required != nil {
		out.Required = slices.Clone(s.Required)
	}
	if s.AdditionalProperties != nil {
		ap := *s.AdditionalProperties
		out.AdditionalProperties = &ap
	}

	if s.isObject() {
		out.applyStrictObjectNormalization(s)
	}
	return out
}

// applyStrictObjectNormalization populates sorted Required keys, nullable optional properties, and additionalProperties=false.
func (s *Schema) applyStrictObjectNormalization(orig *Schema) {
	s.AdditionalProperties = boolPtr(false)
	if len(orig.Properties) == 0 {
		s.Required = []string{}
		return
	}

	reqSet := make(map[string]bool, len(orig.Required))
	for _, k := range orig.Required {
		reqSet[k] = true
	}

	normProps := make(map[string]*Schema, len(orig.Properties))
	allKeys := make([]string, 0, len(orig.Properties))
	for key, prop := range orig.Properties {
		allKeys = append(allKeys, key)
		normProp := prop.Strict()
		if !reqSet[key] {
			normProp = makePropertyNullable(normProp)
		}
		normProps[key] = normProp
	}
	slices.Sort(allKeys)
	s.Properties = normProps
	s.Required = allKeys
}

// strictSchemaSlice normalizes each Schema element in items for strict structured outputs.
func strictSchemaSlice(items []*Schema) []*Schema {
	if items == nil {
		return nil
	}
	out := make([]*Schema, len(items))
	for i, item := range items {
		out[i] = item.Strict()
	}
	return out
}

// isObject reports whether s explicitly declares object type or defines object properties.
func (s *Schema) isObject() bool {
	return s.Type.Contains(TypeObject) || len(s.Properties) > 0
}

// acceptsNull reports whether s already permits null values directly or via an anyOf branch.
func (s *Schema) acceptsNull() bool {
	if s.Type.Contains(TypeNull) {
		return true
	}
	for _, branch := range s.AnyOf {
		if branch != nil && branch.Type.Contains(TypeNull) {
			return true
		}
	}
	return false
}

// makePropertyNullable converts a strict-mode schema node so it accepts null when optional in the canonical schema.
func makePropertyNullable(prop *Schema) *Schema {
	if prop == nil || prop.acceptsNull() {
		return prop
	}
	if len(prop.AnyOf) > 0 {
		prop.AnyOf = append(prop.AnyOf, &Schema{Type: SchemaTypes{TypeNull}})
		return prop
	}
	if len(prop.Enum) > 0 || len(prop.Properties) > 0 || prop.Type.Contains(TypeObject) {
		outerDesc := prop.Description
		prop.Description = ""
		return &Schema{
			Description: outerDesc,
			AnyOf: []*Schema{
				prop,
				{Type: SchemaTypes{TypeNull}},
			},
		}
	}
	if len(prop.Type) > 0 {
		prop.Type = append(slices.Clone(prop.Type), TypeNull)
		return prop
	}
	return &Schema{
		AnyOf: []*Schema{
			prop,
			{Type: SchemaTypes{TypeNull}},
		},
	}
}

// boolPtr allocates a pointer copy of v for optional schema boolean attributes.
func boolPtr(v bool) *bool {
	return &v
}

// stringProp returns a typed string Schema node with desc.
func stringProp(desc string) *Schema {
	return &Schema{Type: SchemaTypes{TypeString}, Description: desc}
}

// boolProp returns a typed boolean Schema node with desc.
func boolProp(desc string) *Schema {
	return &Schema{Type: SchemaTypes{TypeBoolean}, Description: desc}
}

// numberProp returns a typed numeric Schema node with desc.
func numberProp(desc string) *Schema {
	return &Schema{Type: SchemaTypes{TypeNumber}, Description: desc}
}

// arrayProp returns a typed array Schema node containing items.
func arrayProp(desc string, items *Schema) *Schema {
	return &Schema{Type: SchemaTypes{TypeArray}, Description: desc, Items: items}
}

// stringArrayProp returns a typed array-of-string Schema node with desc.
func stringArrayProp(desc string) *Schema {
	return arrayProp(desc, &Schema{Type: SchemaTypes{TypeString}})
}

// objectSchema returns a typed object Schema node with optional required keys.
func objectSchema(desc string, props map[string]*Schema, required ...string) *Schema {
	return &Schema{
		Type:        SchemaTypes{TypeObject},
		Description: desc,
		Properties:  props,
		Required:    required,
	}
}

// strictObjectSchema returns a typed object Schema node with additionalProperties=false and required keys.
func strictObjectSchema(desc string, props map[string]*Schema, required ...string) *Schema {
	s := objectSchema(desc, props, required...)
	s.AdditionalProperties = boolPtr(false)
	return s
}
