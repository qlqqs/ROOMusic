package main

import "github.com/extism/go-pdk"

//go:wasmimport extism:host/user upstream_fetch
func upstreamFetch(uint64) uint64

//go:wasmexport describe
func describe() uint32 {
	pdk.OutputString(`{"capabilities":["lyrics"]}`)
	return 0
}

//go:wasmexport lyrics
func lyrics() uint32 {
	req := pdk.AllocateBytes(pdk.Input())
	defer req.Free()
	reply := pdk.FindMemory(upstreamFetch(req.Offset()))
	defer reply.Free()
	pdk.Output(reply.ReadBytes())
	return 0
}

func main() {}
