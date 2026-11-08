package derivation

import (
	"strings"
	"testing"
	"time"
)

func sampleVersion() SourceVersion {
	return SourceVersion{
		Policy:             policyALACFLAC,
		ContentFingerprint: "m4a-mdat-v1:abc", Size: 1234, ModifiedAt: testTime.Add(123456 * time.Microsecond),
		SourceKind: "m4a", Codec: "alac", BitDepth: 24, SampleRate: 48000, Channels: 2,
	}
}

func TestDeriveKeyIsStableAndHexEncoded(t *testing.T) {
	first := deriveKey("ffmpeg version 7.1.1", sampleVersion())
	second := deriveKey("ffmpeg version 7.1.1", sampleVersion())
	if first != second {
		t.Fatalf("keys differ for identical input: %s / %s", first, second)
	}
	if _, ok := keyFromName(first + entrySuffix); !ok || len(first) != keyLength {
		t.Fatalf("key %q is not a valid cache key", first)
	}
}

func TestDeriveKeyChangesWithEveryIdentifyingInput(t *testing.T) {
	base := deriveKey("ffmpeg version 7.1.1", sampleVersion())
	for name, changed := range map[string]string{
		"policy":            deriveKey("ffmpeg version 7.1.1", withVersion(func(v *SourceVersion) { v.Policy = "alac-flac-lossless-v2" })),
		"converter version": deriveKey("ffmpeg version 7.1.2", sampleVersion()),
		"fingerprint":       deriveKey("ffmpeg version 7.1.1", withVersion(func(v *SourceVersion) { v.ContentFingerprint = "m4a-mdat-v1:abd" })),
		"size":              deriveKey("ffmpeg version 7.1.1", withVersion(func(v *SourceVersion) { v.Size++ })),
		"modified time":     deriveKey("ffmpeg version 7.1.1", withVersion(func(v *SourceVersion) { v.ModifiedAt = v.ModifiedAt.Add(time.Microsecond) })),
		"source kind":       deriveKey("ffmpeg version 7.1.1", withVersion(func(v *SourceVersion) { v.SourceKind = "mp4" })),
		"codec":             deriveKey("ffmpeg version 7.1.1", withVersion(func(v *SourceVersion) { v.Codec = "aac" })),
	} {
		if changed == base {
			t.Errorf("changing the %s did not change the key", name)
		}
	}
}

func TestDeriveKeyIgnoresRepresentationDetails(t *testing.T) {
	base := deriveKey("v", sampleVersion())
	for name, same := range map[string]string{
		"sub-microsecond time": deriveKey("v", withVersion(func(v *SourceVersion) { v.ModifiedAt = v.ModifiedAt.Add(500 * time.Nanosecond) })),
		"time zone":            deriveKey("v", withVersion(func(v *SourceVersion) { v.ModifiedAt = v.ModifiedAt.In(time.FixedZone("x", 8*3600)) })),
		"bit depth":            deriveKey("v", withVersion(func(v *SourceVersion) { v.BitDepth = 16 })),
		"sample rate":          deriveKey("v", withVersion(func(v *SourceVersion) { v.SampleRate = 44100 })),
		"channels":             deriveKey("v", withVersion(func(v *SourceVersion) { v.Channels = 6 })),
	} {
		if same != base {
			t.Errorf("%s changed the key, but it does not identify the source version", name)
		}
	}
}

// 字段带长度前缀：把字段边界挪动不会得到相同的键。
func TestDeriveKeyFieldBoundariesAreUnambiguous(t *testing.T) {
	left := deriveKey("ffmpeg", withVersion(func(v *SourceVersion) { v.ContentFingerprint = "ab"; v.SourceKind = "c" }))
	right := deriveKey("ffmpeg", withVersion(func(v *SourceVersion) { v.ContentFingerprint = "a"; v.SourceKind = "bc" }))
	if left == right {
		t.Fatal("shifting a field boundary produced the same key")
	}
	if strings.Contains(left, "ffmpeg") {
		t.Fatal("the key must be a digest")
	}
}

func withVersion(change func(*SourceVersion)) SourceVersion {
	version := sampleVersion()
	change(&version)
	return version
}

// 旧键由 git show HEAD:backend/internal/playback/derivation/key.go 的固定 ALAC 策略与长度前缀推算，不能用当前实现生成期望值。
func TestDeriveKeyPreservesTheKnownALACCacheKey(t *testing.T) {
	const want = "f89f5c3f5f2ca5843932ac0fcfe5d631c4f5d564b454575b1d87625e204634c4"
	if got := deriveKey("ffmpeg version 7.1.1", sampleVersion()); got != want {
		t.Fatalf("ALAC cache key = %s, want the existing %s", got, want)
	}
}
