package schema

import (
	"bytes"
	"encoding/json"
	"io"
	"reflect"
	"strings"
)

// Decode 按结构体标签逐层读取，要求每个字段恰好出现一次，拒绝 null、大小写变体和尾随数据。
func Decode(r io.Reader) (Payload, error) {
	var p Payload
	raw, err := io.ReadAll(io.LimitReader(r, MaxBytes+1))
	if err != nil || len(raw) > MaxBytes {
		return p, ErrInvalid
	}
	// 先取版本以选择字段集合，字段顺序任意；下方仍严格检查版本键的大小写与重复。
	var header struct {
		Schema int `json:"schema"`
	}
	if err := json.Unmarshal(raw, &header); err != nil {
		return p, ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	if err := readObject(d, reflect.ValueOf(&p).Elem(), header.Schema); err != nil {
		return Payload{}, ErrInvalid
	}
	if _, err := d.Token(); err != io.EOF {
		return Payload{}, ErrInvalid
	}
	return p, p.Validate()
}

func readObject(d *json.Decoder, v reflect.Value, version int) error {
	if t, err := d.Token(); err != nil || t != json.Delim('{') {
		return ErrInvalid
	}
	fields := make(map[string]reflect.Value, v.NumField())
	for i := 0; i < v.NumField(); i++ {
		name, _, _ := strings.Cut(v.Type().Field(i).Tag.Get("json"), ",")
		if v.Type() == reflect.TypeFor[Devices]() && ((version < 2 && name == "cast") || (version < 3 && name == "airplay")) {
			continue
		}
		fields[name] = v.Field(i)
	}
	for d.More() {
		t, err := d.Token()
		key, ok := t.(string)
		field, present := fields[key]
		if err != nil || !ok || !present {
			return ErrInvalid
		}
		delete(fields, key)
		if field.Kind() == reflect.Struct {
			if err := readObject(d, field, version); err != nil {
				return err
			}
			continue
		}
		var raw json.RawMessage
		if d.Decode(&raw) != nil || bytes.Equal(raw, []byte("null")) || json.Unmarshal(raw, field.Addr().Interface()) != nil {
			return ErrInvalid
		}
	}
	if t, err := d.Token(); err != nil || t != json.Delim('}') || len(fields) != 0 {
		return ErrInvalid
	}
	return nil
}
