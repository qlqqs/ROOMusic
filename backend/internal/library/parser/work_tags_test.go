package parser

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/library/parser/parsertest"
)

func TestAudioParsersReadWorkAndMovementTags(t *testing.T) {
	const work = "第五交响曲"
	const movement = "II. Andante con moto"
	for _, fixture := range []struct {
		name string
		data func(t *testing.T) []byte
	}{
		{
			name: "vorbis.flac",
			data: func(t *testing.T) []byte {
				return parsertest.FLACWithComments(t, []string{
					"WORK=" + work,
					"MOVEMENTNAME=" + movement,
					"MOVEMENTNUMBER=2/4",
					"MOVEMENTTOTAL=4",
				})
			},
		},
		{
			name: "id3.mp3",
			data: func(t *testing.T) []byte {
				return workMP3Fixture([][2]string{
					{"TIT1", work},
					{"MVNM", movement},
					{"MVIN", "2/4"},
				})
			},
		},
		{
			name: "itunes.m4a",
			data: func(t *testing.T) []byte {
				items := repairM4AAtom("\xa9wrk", repairM4AData(1, []byte(work)))
				items = append(items, repairM4AAtom("\xa9mvn", repairM4AData(1, []byte(movement)))...)
				items = append(items, repairM4AAtom("\xa9mvi", repairM4AData(21, []byte{0, 2}))...)
				items = append(items, repairM4AAtom("\xa9mvc", repairM4AData(21, []byte{0, 4}))...)
				data := repairM4AAtom("ftyp", []byte("M4A \x00\x00\x00\x00"))
				return append(data, repairM4AMoovWithItems("mp4a", 2, 16, 44100, 88200, "", "", "", "", items)...)
			},
		},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), fixture.name)
			if err := os.WriteFile(path, fixture.data(t), 0o644); err != nil {
				t.Fatalf("写入作品标签素材：%v", err)
			}
			observation, err := ParseAudioFile(path)
			if err != nil {
				t.Fatalf("读取作品标签：%v", err)
			}
			if observation.Work != work || observation.MovementName != movement || observation.MovementNumber != 2 {
				t.Fatalf("作品/乐章 = %q/%q/%d，期望 %q/%q/2", observation.Work, observation.MovementName, observation.MovementNumber, work, movement)
			}
		})
	}
}

func TestID3GroupingWithoutMovementOrCatalogIsNotWork(t *testing.T) {
	path := filepath.Join(t.TempDir(), "grouping.mp3")
	data := workMP3Fixture([][2]string{{"TIT1", "收藏分组"}})
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("写入分组标签素材：%v", err)
	}
	observation, err := ParseAudioFile(path)
	if err != nil {
		t.Fatalf("读取分组标签：%v", err)
	}
	if observation.Work != "" || observation.MovementName != "" || observation.MovementNumber != 0 {
		t.Fatalf("普通 TIT1 分组被当作作品或乐章：%+v", observation)
	}
}

func workMP3Fixture(frames [][2]string) []byte {
	var payload []byte
	for _, frame := range frames {
		encoded := repairMP3BinaryFrame(frame[0], append([]byte{3}, []byte(frame[1])...))
		payload = append(payload, encoded[10:]...)
	}
	size := len(payload)
	header := []byte{'I', 'D', '3', 3, 0, 0, byte(size >> 21 & 0x7f), byte(size >> 14 & 0x7f), byte(size >> 7 & 0x7f), byte(size & 0x7f)}
	return append(header, payload...)
}
