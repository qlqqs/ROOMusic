package main

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunExporterUsesScannerAndRedactsRoot(t *testing.T) {
	root := t.TempDir()
	album := filepath.Join(root, "Example Artist - Example Album [2024]")
	if err := os.Mkdir(album, 0o700); err != nil {
		t.Fatal(err)
	}
	writeTestWAV(t, filepath.Join(album, "01 - Example Track.wav"))
	outputDir := t.TempDir()
	output := filepath.Join(outputDir, "rows.ndjson")
	if err := runExporter(root, output); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	secondOutput := filepath.Join(outputDir, "rows-second.ndjson")
	if err := runExporter(root, secondOutput); err != nil {
		t.Fatal(err)
	}
	secondContent, err := os.ReadFile(secondOutput)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != string(secondContent) {
		t.Fatal("adapter output is not deterministic")
	}
	if strings.Contains(string(content), root) {
		t.Fatal("adapter output leaked the absolute corpus root")
	}

	recordTypes := make([]string, 0)
	scanner := bufio.NewScanner(strings.NewReader(string(content)))
	for scanner.Scan() {
		var record struct {
			RecordType string `json:"record_type"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			t.Fatal(err)
		}
		recordTypes = append(recordTypes, record.RecordType)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if strings.Join(recordTypes, ",") != "header,release,complete" {
		t.Fatalf("unexpected record sequence: %v", recordTypes)
	}
}

func TestRunExporterRejectsUnsafeCueWithoutPartialOutput(t *testing.T) {
	root := t.TempDir()
	album := filepath.Join(root, "Unsafe Cue")
	if err := os.Mkdir(album, 0o700); err != nil {
		t.Fatal(err)
	}
	writeTestWAV(t, filepath.Join(album, "image.wav"))
	cue := `FILE "../escape.wav" WAVE
  TRACK 01 AUDIO
    TITLE "Unsafe"
    INDEX 01 00:00:00
`
	if err := os.WriteFile(filepath.Join(album, "album.cue"), []byte(cue), 0o600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "rows.ndjson")
	if err := runExporter(root, output); err == nil {
		t.Fatal("expected unsafe CUE to fail")
	}
	if _, err := os.Lstat(output); !os.IsNotExist(err) {
		t.Fatalf("failed export left an output artifact: %v", err)
	}
}

func writeTestWAV(t *testing.T, path string) {
	t.Helper()
	const sampleRate = 8000
	const dataSize = 800
	buffer := make([]byte, 44+dataSize)
	copy(buffer[0:4], "RIFF")
	binary.LittleEndian.PutUint32(buffer[4:8], uint32(len(buffer)-8))
	copy(buffer[8:12], "WAVE")
	copy(buffer[12:16], "fmt ")
	binary.LittleEndian.PutUint32(buffer[16:20], 16)
	binary.LittleEndian.PutUint16(buffer[20:22], 1)
	binary.LittleEndian.PutUint16(buffer[22:24], 1)
	binary.LittleEndian.PutUint32(buffer[24:28], sampleRate)
	binary.LittleEndian.PutUint32(buffer[28:32], sampleRate*2)
	binary.LittleEndian.PutUint16(buffer[32:34], 2)
	binary.LittleEndian.PutUint16(buffer[34:36], 16)
	copy(buffer[36:40], "data")
	binary.LittleEndian.PutUint32(buffer[40:44], dataSize)
	if err := os.WriteFile(path, buffer, 0o600); err != nil {
		t.Fatal(err)
	}
}
