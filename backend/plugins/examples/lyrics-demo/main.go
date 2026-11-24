package main

import (
	"encoding/json"

	"github.com/extism/go-pdk"
)

//go:wasmimport extism:host/user upstream_fetch
func upstreamFetch(uint64) uint64

//go:wasmexport describe
func describe() uint32 { pdk.OutputString(`{"capabilities":["lyrics","artist_bio"]}`); return 0 }

//go:wasmexport lyrics
func lyrics() uint32 {
	var q struct{ Title, Artist string }
	if pdk.InputJSON(&q) != nil {
		return 1
	}
	return lookup("lyrics", map[string]string{"title": q.Title, "artist": q.Artist})
}

//go:wasmexport artist_bio
func artistBio() uint32 {
	var q struct{ MBID, Lang string }
	if pdk.InputJSON(&q) != nil {
		return 1
	}
	return lookup("artist_bio", map[string]string{"mbid": q.MBID, "lang": q.Lang})
}

func lookup(path string, q map[string]string) uint32 {
	req, err := pdk.AllocateJSON(map[string]any{"upstream": "main", "path": path, "query": q})
	if err != nil {
		return 2
	}
	defer req.Free()
	ptr := upstreamFetch(req.Offset())
	if ptr == 0 {
		return 2
	}
	out := pdk.FindMemory(ptr)
	defer out.Free()
	var reply struct {
		Status int
		Body   []byte `json:"body_base64"`
	}
	if json.Unmarshal(out.ReadBytes(), &reply) != nil {
		return 2
	}
	switch reply.Status {
	case 404:
		return 1
	case 200:
		pdk.Output(reply.Body)
		return 0
	default:
		return 2
	}
}

func main() {}
