package catalog

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDecodeArtistRefs(t *testing.T) {
	ref := ArtistRef{ArtistID: "a0000000-0000-4000-8000-000000000001", Name: "Jay Chou", DisplayName: "周杰伦"}
	encoded, _ := json.Marshal([]ArtistRef{ref})
	got, err := decodeArtistRefs(encoded)
	if err != nil || len(got) != 1 || got[0] != ref {
		t.Fatalf("原署名必须与主名独立保留: %+v, %v", got, err)
	}
	var fields []map[string]any
	if err := json.Unmarshal(encoded, &fields); err != nil || len(fields[0]) != 3 {
		t.Fatalf("ArtistRef 必须恰好三个字段: %s", encoded)
	}
	withRole := strings.Replace(string(encoded), `"name":"Jay Chou"`, `"name":"Jay Chou","role":"track_artist"`, 1)
	if _, err := decodeArtistRefs([]byte(withRole)); err == nil {
		t.Fatal("role 不属于 ArtistRef 合同")
	}
	empty, err := decodeArtistRefs([]byte("[]"))
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("空数组不能变 null: %+v %v", empty, err)
	}
	for name, mutate := range map[string]func(*ArtistRef){
		"id":      func(r *ArtistRef) { r.ArtistID = "wrong" },
		"name":    func(r *ArtistRef) { r.Name = " " },
		"display": func(r *ArtistRef) { r.DisplayName = "" }, "control": func(r *ArtistRef) { r.Name = "A\nB" },
		"oversize": func(r *ArtistRef) { r.Name = strings.Repeat("a", 4097) },
	} {
		t.Run(name, func(t *testing.T) {
			bad := ref
			mutate(&bad)
			data, _ := json.Marshal([]ArtistRef{bad})
			if _, err := decodeArtistRefs(data); err == nil {
				t.Fatal("坏引用应拒绝")
			}
		})
	}
	for _, data := range []string{"", "null", "{}", "[null]", "[{}]", "[] []", string(encoded[:len(encoded)-1]), "[\xff]"} {
		if _, err := decodeArtistRefs([]byte(data)); err == nil {
			t.Fatalf("坏 JSON 通过: %q", data)
		}
	}
	repeated, _ := json.Marshal([]ArtistRef{ref, ref})
	if _, err := decodeArtistRefs(repeated); err == nil {
		t.Fatal("重复引用未拒绝")
	}
	tooMany := make([]ArtistRef, maxArtistRefs+1)
	for i := range tooMany {
		tooMany[i] = ref
	}
	data, _ := json.Marshal(tooMany)
	if _, err := decodeArtistRefs(data); err == nil {
		t.Fatal("超量引用未拒绝")
	}
}
