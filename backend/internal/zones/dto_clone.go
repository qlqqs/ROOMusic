package zones

import (
	"slices"

	"github.com/qlqq/roomusic/backend/internal/catalog"
)

// DTO 交给调用方后不再共享运行时的可变指针或切片。
func cloneValue[T any](in *T) *T {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

func cloneTrack(in catalog.TrackSummary) catalog.TrackSummary {
	in.ArtistRefs = slices.Clone(in.ArtistRefs)
	in.DurationSeconds = cloneValue(in.DurationSeconds)
	in.Source.Codec = cloneValue(in.Source.Codec)
	in.Source.BitDepth = cloneValue(in.Source.BitDepth)
	in.Source.SampleRate = cloneValue(in.Source.SampleRate)
	in.Source.Channels = cloneValue(in.Source.Channels)
	in.Source.Bitrate = cloneValue(in.Source.Bitrate)
	in.Release.ArtistRefs = slices.Clone(in.Release.ArtistRefs)
	in.Release.AlbumArtistRefs = slices.Clone(in.Release.AlbumArtistRefs)
	in.Release.AlbumArtist = cloneValue(in.Release.AlbumArtist)
	in.Release.Year = cloneValue(in.Release.Year)
	in.Release.SourceType = cloneValue(in.Release.SourceType)
	in.Release.MediaType = cloneValue(in.Release.MediaType)
	in.Release.Artwork = cloneValue(in.Release.Artwork)
	return in
}

func cloneStream(in *Stream) *Stream {
	if in == nil {
		return nil
	}
	out := *in
	out.Format = cloneValue(in.Format)
	if out.Format != nil {
		out.Format.BitDepth = cloneValue(in.Format.BitDepth)
	}
	out.Segments = append([]StreamSegment{}, in.Segments...)
	for i := range out.Segments {
		out.Segments[i].Track = cloneTrack(out.Segments[i].Track)
	}
	return &out
}

func cloneCapabilities(in Capabilities) Capabilities {
	in.StreamFormats = append([]StreamFormatCapability{}, in.StreamFormats...)
	for i := range in.StreamFormats {
		in.StreamFormats[i].MaxSampleRate = cloneValue(in.StreamFormats[i].MaxSampleRate)
		in.StreamFormats[i].MaxBitDepth = cloneValue(in.StreamFormats[i].MaxBitDepth)
	}
	return in
}

func cloneZoneEvent(in *ZoneEvent) *ZoneEvent {
	out := cloneValue(in)
	if out != nil {
		out.Restored = cloneValue(in.Restored)
		out.Dropped = cloneValue(in.Dropped)
	}
	return out
}
