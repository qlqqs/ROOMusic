package main

import "github.com/extism/go-pdk"

//go:wasmexport builtin_http
func builtinHTTP() uint32 {
	var address string
	if pdk.InputJSON(&address) != nil {
		return 3
	}
	pdk.NewHTTPRequest(pdk.MethodGet, address).Send()
	pdk.OutputString(`{}`)
	return 0
}

func main() {}
