package zones

import (
	"encoding/json"
	"strings"
)

// ConvolutionSettings 是调用方独占的临时配置；共享设置仅保存不可变规范文档。
type ConvolutionSettings struct {
	Enabled   bool    `json:"enabled"`
	IRID      *string `json:"ir_id"`
	BeforePEQ bool    `json:"before_peq"`
}

func DefaultConvolutionDocument() SettingsDocument {
	document, err := NewConvolutionDocument(ConvolutionSettings{})
	if err != nil {
		panic("默认卷积设置无效")
	}
	return document
}

func NewConvolutionDocument(settings ConvolutionSettings) (SettingsDocument, error) {
	return newSettingsDocument(settings, func(settings ConvolutionSettings) (ConvolutionSettings, error) {
		if settings.IRID != nil {
			if !validID(*settings.IRID) {
				return ConvolutionSettings{}, ErrInvalidInput
			}
			id := strings.ToLower(*settings.IRID)
			settings.IRID = &id
		}
		return settings, nil
	})
}

// ParseConvolutionDocument 要求完整对象，仅 ir_id 可以为 null。
func ParseConvolutionDocument(raw []byte) (SettingsDocument, error) {
	if !validConvolutionShape(raw) {
		return SettingsDocument{}, ErrInvalidInput
	}
	var settings ConvolutionSettings
	if err := json.Unmarshal(raw, &settings); err != nil {
		return SettingsDocument{}, ErrInvalidInput
	}
	return NewConvolutionDocument(settings)
}

// DecodeConvolution 每次返回独占的指针，不把共享文档暴露为可变配置。
func (d SettingsDocument) DecodeConvolution() (ConvolutionSettings, error) {
	document, err := ParseConvolutionDocument([]byte(d.canonical))
	if err != nil {
		return ConvolutionSettings{}, err
	}
	return DecodeSettingsDocument[ConvolutionSettings](document)
}

func validConvolutionShape(raw []byte) bool {
	fields, ok := settingsObject(raw)
	if !ok || len(fields) != 3 {
		return false
	}
	for _, key := range []string{"enabled", "ir_id", "before_peq"} {
		value, exists := fields[key]
		if !exists || (key != "ir_id" && isSettingsNull(value)) {
			return false
		}
	}
	return true
}
