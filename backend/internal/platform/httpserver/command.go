package httpserver

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"unicode/utf8"
)

func DecodeCommand(w http.ResponseWriter, r *http.Request, destination any) ([]byte, bool) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
	if err != nil || !utf8.Valid(raw) {
		WriteError(w, r, 400, "invalid_json", "请求格式无效或超过预算")
		return nil, false
	}
	if err := UniqueJSONObject(raw); err != nil {
		WriteError(w, r, 400, "invalid_json", "请求格式无效")
		return nil, false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		WriteError(w, r, 400, "invalid_json", "请求格式无效")
		return nil, false
	}
	return raw, true
}

func UniqueJSONObject(raw []byte) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return errors.New("expected object")
	}
	if err := jsonContainer(d, '{'); err != nil {
		return err
	}
	if _, err = d.Token(); err != io.EOF {
		return errors.New("trailing JSON")
	}
	return nil
}
func jsonContainer(d *json.Decoder, kind json.Delim) error {
	keys := map[string]bool{}
	for d.More() {
		if kind == '{' {
			token, err := d.Token()
			if err != nil {
				return err
			}
			key, ok := token.(string)
			if !ok || keys[key] {
				return errors.New("duplicate JSON key")
			}
			keys[key] = true
		}
		token, err := d.Token()
		if err != nil {
			return err
		}
		if delimiter, ok := token.(json.Delim); ok {
			if delimiter != '{' && delimiter != '[' {
				return errors.New("invalid JSON")
			}
			if err := jsonContainer(d, delimiter); err != nil {
				return err
			}
		}
	}
	end, err := d.Token()
	if err != nil {
		return err
	}
	if (kind == '{' && end != json.Delim('}')) || (kind == '[' && end != json.Delim(']')) {
		return errors.New("invalid JSON")
	}
	return nil
}
