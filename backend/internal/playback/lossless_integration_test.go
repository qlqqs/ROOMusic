package playback

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/playback/derivation"
)

type recordingLosslessDerivation struct {
	stubDerivation
	requests []derivation.Request
	opens    int
}

func (d *recordingLosslessDerivation) Await(_ context.Context, request derivation.Request) (derivation.Entry, error) {
	d.requests = append(d.requests, request)
	return derivation.Entry{Key: "cached", Size: 64}, nil
}

func (d *recordingLosslessDerivation) OpenEntry(string) (*os.File, int64, error) {
	d.opens++
	return nil, 0, derivation.ErrNotCached
}

func TestPostgreSQLPlaybackBitDepthGateAndLosslessInputFacts(t *testing.T) {
	fixture := newPlaybackFixture(t)
	for _, tc := range []struct {
		sourceKind, codec, policy string
		input                     derivation.Input
		content                   []byte
	}{
		{"m4a", "alac", "alac-flac-lossless-v1", derivation.Input{Demuxer: "mov", Decoder: "alac"}, atom("ftyp", 4)},
		{"ape_apev2", "ape", "ape-flac-lossless-v1", derivation.Input{Demuxer: "ape", Decoder: "ape"}, []byte("MAC ")},
		{"wavpack_apev2", "wavpack", "wavpack-flac-lossless-v1", derivation.Input{Demuxer: "wv", Decoder: "wavpack"}, []byte("wvpk")},
		{"wavpack_apev2", "wavpack_hybrid", "wavpack-flac-lossless-v1", derivation.Input{Demuxer: "wv", Decoder: "wavpack"}, []byte("wvpk")},
		{"aiff_id3", "aiff", "aiff-flac-lossless-v1", derivation.Input{Demuxer: "aiff"}, []byte("FORM\x00\x00\x00\x04AIFF")},
	} {
		for _, depth := range []int{0, 16, 24, 32} {
			t.Run(fmt.Sprintf("%s/%d", tc.codec, depth), func(t *testing.T) {
				relative := fmt.Sprintf("%s-%d.source", tc.codec, depth)
				writeSource(t, fixture.musicRoot, relative, tc.content, scanTime)
				size, modified := int64(len(tc.content)), scanTime.UTC().Truncate(time.Microsecond)
				trackID := fixture.insertTrack(t, trackSpec{
					relativePath: relative, sourceKind: tc.sourceKind, codec: textPointer(tc.codec),
					size: &size, modifiedAt: &modified, fingerprint: "lossless-test-fingerprint",
					bitDepth: depth, sampleRate: 48000, channels: 2,
				})
				probe := &recordingLosslessDerivation{}
				allowed := []string{fixture.allowedRoot}
				if depth == 32 {
					// 允许根为空会令路径核对失败；位深关卡必须先于它返回不能播放。
					allowed = nil
				}
				service := NewService(fixture.database, allowed, WithDerivation(probe))
				representation, err := service.Preflight(context.Background(), trackID, "user", time.Second)
				if depth == 32 {
					if !errors.Is(err, ErrNotDirectlyPlayable) {
						t.Fatalf("Preflight = %v, want ErrNotDirectlyPlayable before path validation", err)
					}
				} else {
					if err != nil || representation.Format.Delivery != DeliveryDerived || representation.Size != 64 {
						t.Fatalf("Preflight = %+v, %v", representation, err)
					}
					if len(probe.requests) != 1 {
						t.Fatalf("accepted source did not reach derivation: %v", probe.requests)
					}
					source := probe.requests[0].Source
					wantVersion := derivation.SourceVersion{
						Policy: tc.policy, ContentFingerprint: "lossless-test-fingerprint", Size: size, ModifiedAt: modified,
						SourceKind: tc.sourceKind, Codec: tc.codec, BitDepth: depth, SampleRate: 48000, Channels: 2,
					}
					gotVersion := source.Version
					if !gotVersion.ModifiedAt.Equal(modified) {
						t.Fatalf("source modification time = %v, want %v", gotVersion.ModifiedAt, modified)
					}
					gotVersion.ModifiedAt = modified
					if source.Input != tc.input || gotVersion != wantVersion {
						t.Fatalf("derivation source = %+v / %+v, want %+v / %+v", source.Input, source.Version, tc.input, wantVersion)
					}
				}
				prepared, err := service.Prepare(context.Background(), trackID)
				if prepared != nil {
					_ = prepared.File.Close()
					t.Fatal("Prepare unexpectedly returned an entry")
				}
				if depth == 32 {
					if !errors.Is(err, ErrNotDirectlyPlayable) || len(probe.requests) != 0 || probe.opens != 0 {
						t.Fatalf("32-bit source reached derivation: %v, awaits %d, opens %d", err, len(probe.requests), probe.opens)
					}
				} else if !errors.Is(err, ErrDerivationNotReady) || probe.opens != 1 {
					t.Fatalf("accepted depth did not reach the cache: %v, opens %d", err, probe.opens)
				}
			})
		}
	}
}
