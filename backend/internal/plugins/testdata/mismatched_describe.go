package main

import "github.com/extism/go-pdk"

//go:wasmexport describe
func describe() uint32 {
	pdk.OutputString(`{"capabilities":["artist_bio"]}`)
	return 0
}

//go:wasmexport lyrics
func lyrics() uint32 { return 1 }

func main() {}
