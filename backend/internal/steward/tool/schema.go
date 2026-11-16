package tool

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
)

type schema struct {
	typ                  string
	properties           map[string]*schema
	required             []string
	additionalProperties bool
	enum                 []any
	items                *schema
	minimum, maximum     *big.Rat
	minLength, maxLength *big.Rat
}

func compileSchema(raw json.RawMessage) (*schema, error) {
	value, err := decodeJSON(raw)
	if err != nil {
		return nil, err
	}
	return compileSchemaNode(value, "$")
}

func compileSchemaNode(value any, path string) (*schema, error) {
	object, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s: schema must be a JSON object", path)
	}
	compiled := &schema{additionalProperties: true}
	for _, keyword := range sortedKeys(object) {
		value := object[keyword]
		location := path + "." + keyword
		switch keyword {
		case "type":
			typ, ok := value.(string)
			if !ok || !supportedType(typ) {
				return nil, fmt.Errorf("%s: expected a supported type name", location)
			}
			compiled.typ = typ
		case "properties":
			properties, ok := value.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("%s: expected an object", location)
			}
			compiled.properties = make(map[string]*schema, len(properties))
			for _, name := range sortedKeys(properties) {
				child, err := compileSchemaNode(properties[name], fmt.Sprintf("%s[%q]", location, name))
				if err != nil {
					return nil, err
				}
				compiled.properties[name] = child
			}
		case "required":
			entries, ok := value.([]any)
			if !ok {
				return nil, fmt.Errorf("%s: expected an array of unique strings", location)
			}
			seen := make(map[string]bool, len(entries))
			for _, entry := range entries {
				name, ok := entry.(string)
				if !ok || seen[name] {
					return nil, fmt.Errorf("%s: expected an array of unique strings", location)
				}
				seen[name] = true
				compiled.required = append(compiled.required, name)
			}
		case "additionalProperties":
			allow, ok := value.(bool)
			if !ok || allow {
				return nil, fmt.Errorf("%s: only false is supported; omit to allow additional properties", location)
			}
			compiled.additionalProperties = false
		case "enum":
			entries, ok := value.([]any)
			if !ok || len(entries) == 0 {
				return nil, fmt.Errorf("%s: expected a nonempty array", location)
			}
			for i, entry := range entries {
				for _, previous := range entries[:i] {
					if equalJSON(entry, previous) {
						return nil, fmt.Errorf("%s: enum values must be unique", location)
					}
				}
			}
			compiled.enum = entries
		case "items":
			child, err := compileSchemaNode(value, location)
			if err != nil {
				return nil, err
			}
			compiled.items = child
		case "minimum", "maximum", "minLength", "maxLength":
			number, ok := value.(*big.Rat)
			if !ok {
				return nil, fmt.Errorf("%s: expected a number", location)
			}
			if (keyword == "minLength" || keyword == "maxLength") && (!number.IsInt() || number.Sign() < 0) {
				return nil, fmt.Errorf("%s: expected a nonnegative integer", location)
			}
			switch keyword {
			case "minimum":
				compiled.minimum = number
			case "maximum":
				compiled.maximum = number
			case "minLength":
				compiled.minLength = number
			case "maxLength":
				compiled.maxLength = number
			}
		default:
			return nil, fmt.Errorf("%s: unsupported schema keyword", location)
		}
	}
	return compiled, nil
}

func supportedType(typ string) bool {
	switch typ {
	case "object", "array", "string", "number", "integer", "boolean", "null":
		return true
	default:
		return false
	}
}

func decodeJSON(raw json.RawMessage) (any, error) {
	if !json.Valid(raw) {
		return nil, fmt.Errorf("expected one valid JSON value")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return exactNumbers(value)
}

// exactNumbers 避免 float64 舍入让大整数、边界与枚举比较失真。
func exactNumbers(value any) (any, error) {
	switch value := value.(type) {
	case json.Number:
		number, ok := new(big.Rat).SetString(string(value))
		if !ok {
			return nil, fmt.Errorf("JSON number exponent is out of range")
		}
		return number, nil
	case map[string]any:
		for name, item := range value {
			normalized, err := exactNumbers(item)
			if err != nil {
				return nil, err
			}
			value[name] = normalized
		}
	case []any:
		for i, item := range value {
			normalized, err := exactNumbers(item)
			if err != nil {
				return nil, err
			}
			value[i] = normalized
		}
	}
	return value, nil
}

func sortedKeys(object map[string]any) []string {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
