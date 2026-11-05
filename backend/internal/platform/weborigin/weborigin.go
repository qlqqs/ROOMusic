// Package weborigin 提供配置与请求共用的 HTTP 来源解析和规范化，不决定来源是否可信。
package weborigin

import (
	"errors"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"unicode"
)

// ParseConfigured 规范化配置中的来源；允许单独的末尾斜杠，调用方负责去掉项前后的空白。
func ParseConfigured(origin string) (string, error) {
	return parse(origin, true)
}

// ParseRequest 规范化请求的 Origin；不允许任何路径，包括末尾斜杠。
func ParseRequest(origin string) (string, error) {
	return parse(origin, false)
}

// FromHost 按来源协议规范化请求 Host；拒绝能改变 URL 结构的字符，不读取转发头。
func FromHost(scheme, host string) (string, error) {
	if (scheme != "http" && scheme != "https") || host == "" || strings.ContainsAny(host, "/\\?#@%") {
		return "", errors.New("invalid origin scheme or request host")
	}
	for _, character := range host {
		if unicode.IsSpace(character) || unicode.IsControl(character) {
			return "", errors.New("request host must not contain whitespace or control characters")
		}
	}
	return ParseRequest(scheme + "://" + host)
}

func parse(origin string, allowTrailingSlash bool) (string, error) {
	invalid := errors.New("must be http(s)://host[:port] without path, query, fragment or credentials")
	parsed, err := url.Parse(origin)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || strings.ContainsAny(origin, "?#") {
		return "", invalid
	}
	if parsed.RawPath != "" || (parsed.Path != "" && !(allowTrailingSlash && parsed.Path == "/")) {
		return "", invalid
	}
	host := parsed.Hostname()
	if host == "" || strings.Contains(host, "*") {
		return "", invalid
	}
	for i := 0; i < len(host); i++ {
		if host[i] >= 0x80 {
			return "", invalid
		}
	}
	host = strings.ToLower(host)
	if strings.HasPrefix(parsed.Host, "[") {
		address, err := netip.ParseAddr(host)
		if err != nil || !address.Is6() || address.Zone() != "" {
			return "", invalid
		}
		host = "[" + host + "]"
	} else if strings.ContainsAny(host, ":[]") {
		return "", invalid
	}
	port := parsed.Port()
	if port == "" && strings.HasSuffix(parsed.Host, ":") {
		return "", invalid
	}
	if port != "" {
		for i := 0; i < len(port); i++ {
			if port[i] < '0' || port[i] > '9' {
				return "", invalid
			}
		}
		number, err := strconv.ParseUint(port, 10, 16)
		if err != nil || number == 0 {
			return "", invalid
		}
		if !((parsed.Scheme == "http" && number == 80) || (parsed.Scheme == "https" && number == 443)) {
			host += ":" + strconv.FormatUint(number, 10)
		}
	}
	return parsed.Scheme + "://" + host, nil
}
