package httpapi

import (
	"bytes"
	"encoding/json"
)

// present 用于协议要求必须出现、但允许 null 的字段。
func (input inputObject) present(names ...string) bool {
	for _, name := range names {
		if _, exists := input[name]; !exists {
			return false
		}
	}
	return true
}

func validClaimFields(fields inputObject) bool {
	if !fields.only("protocol", "capabilities", "takeover", "playing") ||
		!fields.required("protocol", "capabilities", "takeover") || !fields.present("playing") {
		return false
	}
	if !validCapabilitiesFields(fields["capabilities"]) {
		return false
	}
	if raw := fields["playing"]; !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		var playing inputObject
		if json.Unmarshal(raw, &playing) != nil || !playing.only("entry_id", "position_seconds") ||
			!playing.required("entry_id", "position_seconds") {
			return false
		}
	}
	return true
}

// 能力取值与协议版本由 Service 校验；这里只防止缺失、null 与未知键绕过结构体零值。
func validCapabilitiesFields(raw json.RawMessage) bool {
	var capabilities inputObject
	if json.Unmarshal(raw, &capabilities) != nil ||
		!capabilities.only("stream_formats", "max_channels", "dsd", "exclusive", "volume") ||
		!capabilities.required("stream_formats", "max_channels", "dsd", "exclusive", "volume") {
		return false
	}
	var formats []inputObject
	if json.Unmarshal(capabilities["stream_formats"], &formats) != nil {
		return false
	}
	for _, format := range formats {
		if !format.only("codec", "mime_type", "lossless", "max_sample_rate", "max_bit_depth") ||
			!format.required("codec", "mime_type", "lossless") || !format.present("max_sample_rate", "max_bit_depth") {
			return false
		}
	}
	return true
}
