package tool

import (
	"encoding/json"
	"fmt"
	"math/big"
	"unicode/utf8"
)

func (schema *schema) validate(value any, path string) error {
	if schema.typ != "" && !matchesType(value, schema.typ) {
		return fmt.Errorf("%s: 输入类型必须是 %s", path, schema.typ)
	}
	if schema.enum != nil {
		found := false
		for _, allowed := range schema.enum {
			if equalJSON(value, allowed) {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("%s: 输入不属于允许的枚举值", path)
		}
	}
	// JSON Schema 的类型专属关键字只约束对应类型；要禁止其他类型，须声明 type。
	switch value := value.(type) {
	case map[string]any:
		return schema.validateObject(value, path)
	case []any:
		if schema.maxItems != nil && new(big.Rat).SetInt64(int64(len(value))).Cmp(schema.maxItems) > 0 {
			return fmt.Errorf("%s: 元素数超过 maxItems", path)
		}
		if schema.items != nil {
			for i, item := range value {
				if err := schema.items.validate(item, fmt.Sprintf("%s[%d]", path, i)); err != nil {
					return err
				}
			}
		}
	case string:
		length := new(big.Rat).SetInt64(int64(utf8.RuneCountInString(value)))
		if schema.minLength != nil && length.Cmp(schema.minLength) < 0 {
			return fmt.Errorf("%s: 字符数少于 minLength", path)
		}
		if schema.maxLength != nil && length.Cmp(schema.maxLength) > 0 {
			return fmt.Errorf("%s: 字符数超过 maxLength", path)
		}
	case *big.Rat:
		if schema.minimum != nil && value.Cmp(schema.minimum) < 0 {
			return fmt.Errorf("%s: 数值小于 minimum", path)
		}
		if schema.maximum != nil && value.Cmp(schema.maximum) > 0 {
			return fmt.Errorf("%s: 数值大于 maximum", path)
		}
	}
	return nil
}

func (schema *schema) validateObject(value map[string]any, path string) error {
	for _, name := range schema.required {
		if _, exists := value[name]; !exists {
			return fmt.Errorf("%s[%q]: 缺少必填属性", path, name)
		}
	}
	for _, name := range sortedKeys(value) {
		property, exists := schema.properties[name]
		if !exists {
			if !schema.additionalProperties {
				return fmt.Errorf("%s: 不允许未声明的属性", path)
			}
			continue
		}
		if err := property.validate(value[name], fmt.Sprintf("%s[%q]", path, name)); err != nil {
			return err
		}
	}
	return nil
}

func matchesType(value any, typ string) bool {
	switch value := value.(type) {
	case nil:
		return typ == "null"
	case bool:
		return typ == "boolean"
	case string:
		return typ == "string"
	case *big.Rat:
		return typ == "number" || typ == "integer" && value.IsInt()
	case map[string]any:
		return typ == "object"
	case []any:
		return typ == "array"
	default:
		return false
	}
}

func equalJSON(left, right any) bool {
	switch left := left.(type) {
	case nil:
		return right == nil
	case bool:
		right, ok := right.(bool)
		return ok && left == right
	case string:
		right, ok := right.(string)
		return ok && left == right
	case *big.Rat:
		right, ok := right.(*big.Rat)
		return ok && left.Cmp(right) == 0
	case []any:
		right, ok := right.([]any)
		if !ok || len(left) != len(right) {
			return false
		}
		for i, value := range left {
			if !equalJSON(value, right[i]) {
				return false
			}
		}
		return true
	case map[string]any:
		right, ok := right.(map[string]any)
		if !ok || len(left) != len(right) {
			return false
		}
		for name, value := range left {
			other, exists := right[name]
			if !exists || !equalJSON(value, other) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

// ValidateInput 对独立审查工具使用与注册工具相同的 schema 校验边界。
func ValidateInput(rawSchema, input json.RawMessage) error {
	compiled, err := compileSchema(rawSchema)
	if err != nil {
		return err
	}
	value, err := decodeJSON(input)
	if err != nil {
		return err
	}
	return compiled.validate(value, "$")
}
