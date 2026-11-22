package library

import (
	"slices"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
	"github.com/qlqq/roomusic/backend/internal/library/parser"
)

func TestBuildSourceObservationsWithIdentifiers(t *testing.T) {
	album := []metadata.Identifier{
		{Kind: "mb_release", Value: "a1234567-0000-3000-8000-000000000001"},
		{Kind: "mb_release_group", Value: "a1234567-0000-3000-8000-000000000002"},
		{Kind: "mb_album_artist", Value: "a1234567-0000-3000-8000-000000000003"},
		{Kind: "mb_disc", Value: "AbCdEf0123456789._-abcdefgh-"},
	}
	all := []metadata.Identifier{
		album[0],
		{Kind: "mb_recording", Value: "a1234567-0000-3000-8000-000000000004"},
		album[1],
		{Kind: "mb_track", Value: "a1234567-0000-3000-8000-000000000005"},
		{Kind: "mb_artist", Value: "a1234567-0000-3000-8000-000000000006"},
		album[2],
		{Kind: "mb_work", Value: "a1234567-0000-3000-8000-000000000007"},
		{Kind: "acoustid", Value: "a1234567-0000-3000-8000-000000000008"},
		album[3],
		{Kind: "isrc", Value: "USX1Z2500001"},
	}
	parent := parser.AudioObservation{Identifiers: slices.Clone(all)}
	physical := buildPhysicalSourceObservation(parent, "Album/image.flac")
	if !slices.Equal(physical.Identifiers, all) {
		t.Errorf("物理轨标识 = %+v，期望 %+v", physical.Identifiers, all)
	}
	cue := parser.CueTrack{ReferencedFile: "image.flac", Number: 2, ISRC: "GBA1B2500003"}
	virtual := buildCueVirtualObservation(parent, parser.CueDocument{}, cue, "Album/image.cue")
	if !slices.Equal(virtual.Identifiers, album) {
		t.Errorf("虚拟轨标识 = %+v，期望 %+v", virtual.Identifiers, album)
	}
	if virtual.CueISRC != cue.ISRC {
		t.Errorf("虚拟轨 CueISRC = %q，期望 %q", virtual.CueISRC, cue.ISRC)
	}
	if !slices.Equal(parent.Identifiers, all) {
		t.Errorf("构造观察后父文件标识变为 %+v，期望 %+v", parent.Identifiers, all)
	}
}
