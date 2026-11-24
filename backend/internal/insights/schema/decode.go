package schema

import (
	"bytes"
	"encoding/json"
	"io"
	"reflect"
)

// Decode 按结构体标签逐层读取，要求每个字段恰好出现一次，拒绝 null、大小写变体和尾随数据。
func Decode(r io.Reader) (Payload, error) {
	var p Payload
	raw, err := io.ReadAll(io.LimitReader(r, MaxBytes+1))
	if err != nil || len(raw) > MaxBytes {
		return p, ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	if err := readObject(d, reflect.ValueOf(&p).Elem()); err != nil {
		return Payload{}, ErrInvalid
	}
	if _, err := d.Token(); err != io.EOF {
		return Payload{}, ErrInvalid
	}
	return p, p.Validate()
}

func readObject(d *json.Decoder, v reflect.Value) error {
	if t, err := d.Token(); err != nil || t != json.Delim('{') {
		return ErrInvalid
	}
	fields := make(map[string]reflect.Value, v.NumField())
	for i := 0; i < v.NumField(); i++ {
		fields[v.Type().Field(i).Tag.Get("json")] = v.Field(i)
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
			if err := readObject(d, field); err != nil {
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
