package parser

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
)

func apeMD5Fingerprint(header apeHeader) string {
	if header.frameStart == header.frameEnd || allZero(header.md5[:]) {
		return ""
	}
	return fmt.Sprintf("%s:%x:%d:%d:%d:%d", fingerprintAPEMD5, header.md5, header.totalSamples, header.sampleRate, header.channels, header.bitDepth)
}

func apeFingerprint(ctx context.Context, reader io.ReaderAt, size int64) (string, error) {
	header, err := readAPEHeader(reader, size)
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if header.frameStart == header.frameEnd {
		return "", nil
	}
	if fingerprint := apeMD5Fingerprint(header); fingerprint != "" {
		return fingerprint, nil
	}
	hash := sha256.New()
	var facts [20]byte
	binary.BigEndian.PutUint64(facts[:8], header.totalSamples)
	binary.BigEndian.PutUint32(facts[8:12], uint32(header.sampleRate))
	binary.BigEndian.PutUint32(facts[12:16], uint32(header.channels))
	binary.BigEndian.PutUint32(facts[16:20], uint32(header.bitDepth))
	_, _ = hash.Write(facts[:])
	if err := hashSection(ctx, hash, reader, header.frameStart, header.frameEnd); err != nil {
		return "", err
	}
	return formatFingerprint(fingerprintAPEFrames, hash.Sum(nil)), nil
}

func wavpackFingerprint(ctx context.Context, reader io.ReaderAt, size int64) (string, error) {
	var end int64
	var hasAudio bool
	if err := walkWavPackBlocks(ctx, reader, size, func(block wavpackBlock) (bool, error) {
		end = block.end
		hasAudio = hasAudio || block.samples > 0 && block.end-block.start > 32
		return false, nil
	}); err != nil {
		return "", err
	}
	if !hasAudio {
		return "", nil
	}
	hash := sha256.New()
	if err := hashSection(ctx, hash, reader, 0, end); err != nil {
		return "", err
	}
	return formatFingerprint(fingerprintWavPackBlocks, hash.Sum(nil)), nil
}

func aiffFingerprint(ctx context.Context, reader io.ReaderAt, size int64) (string, error) {
	var comm []byte
	if err := walkAIFFChunks(ctx, reader, size, func(chunk aiffChunk, compressed bool) error {
		if chunk.id != "COMM" {
			return nil
		}
		if comm != nil {
			return fmt.Errorf("duplicate AIFF COMM chunk")
		}
		var err error
		comm, err = readAIFFCOMM(reader, chunk, compressed)
		return err
	}); err != nil {
		return "", err
	}
	if comm == nil {
		return "", fmt.Errorf("missing AIFF COMM chunk")
	}
	var observation AudioObservation
	if err := applyAIFFCOMM(comm, &observation); err != nil {
		return "", err
	}
	hash := sha256.New()
	_, _ = hash.Write(comm)
	var foundSound bool
	var audioBytes int64
	if err := walkAIFFChunks(ctx, reader, size, func(chunk aiffChunk, _ bool) error {
		if chunk.id != "SSND" {
			return nil
		}
		foundSound = true
		start, end, err := aiffSoundRange(reader, chunk)
		if err != nil {
			return err
		}
		if err := hashSection(ctx, hash, reader, start, end); err != nil {
			return err
		}
		audioBytes += end - start
		return nil
	}); err != nil {
		return "", err
	}
	if !foundSound {
		return "", fmt.Errorf("missing AIFF SSND chunk")
	}
	if audioBytes == 0 {
		return "", nil
	}
	return formatFingerprint(fingerprintAIFFSound, hash.Sum(nil)), nil
}
