package library

import (
	"context"
	"errors"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/library/parser"
)

// unknownFingerprints 让规划把每个文件都视为没有可识别的内容，供不关心指纹的测试使用。
type unknownFingerprints struct{}

func (unknownFingerprints) Fingerprint(context.Context, string) (string, error) { return "", nil }

// planStagedObservations 对已暂存（含 CUE scope 对齐）的观察做一次遍历完整的规划，指纹一律未知。
func planStagedObservations(t *testing.T, fixture *libraryFixture, scanID string, root registeredRoot) {
	t.Helper()
	if _, err := catalog.PlanStagedRoot(context.Background(), fixture.database, scanID, root.ID, catalog.PlanOptions{WalkComplete: true, Fingerprinter: unknownFingerprints{}}); err != nil {
		t.Fatalf("plan staged observations: %v", err)
	}
}

// persistTestObservations 让直接构造的观察走与扫描相同的“暂存 → 对齐 → 规划 → 写入”顺序，
// 结束后清理暂存行，使同一个 scanID 可以重复使用。写入错误原样返回。
func persistTestObservations(ctx context.Context, service *Service, fixture *libraryFixture, scanID string, root registeredRoot, observations ...catalog.SourceObservation) error {
	for _, observation := range observations {
		if err := stageObservation(ctx, fixture.database, scanID, root.ID, observation); err != nil {
			return err
		}
	}
	if err := catalog.AlignCueStagingScopes(ctx, fixture.database, scanID, root.ID); err != nil {
		return err
	}
	if _, err := catalog.PlanStagedRoot(ctx, fixture.database, scanID, root.ID, catalog.PlanOptions{WalkComplete: true, Fingerprinter: unknownFingerprints{}}); err != nil {
		return err
	}
	persistErr := service.persistStagedCandidates(ctx, fixture.database, scanID, root)
	return errors.Join(persistErr, catalog.ClearStagedObservations(ctx, fixture.database, scanID, root.ID))
}

// saveAudioObservation 把解析器的观察转成暂存观察并写入，与扫描遍历时的转换一致（不含来源文件事实）。
func saveAudioObservation(ctx context.Context, service *Service, fixture *libraryFixture, scanID string, root registeredRoot, relativePath string, observation parser.AudioObservation) error {
	sourceIdentity, err := createTrackSourceIdentity(root.ID, relativePath)
	if err != nil {
		return err
	}
	organized := catalog.SourceObservation{RelativePath: sourceIdentity.RelativePath, Directory: catalog.DirectoryOf(sourceIdentity.RelativePath), Title: observation.Title, Album: observation.Album, AlbumArtist: observation.AlbumArtist, Artist: observation.Artist, SourceType: observation.SourceType, MediaType: observation.MediaType, Genre: observation.Genre, CatalogNumber: observation.Catalog, Year: observation.Year, TrackNumber: observation.TrackNumber, DiscNumber: observation.DiscNumber, SourceKind: observation.SourceKind, InferredFields: observation.InferredFields, FieldSources: observationFieldSources(observation), DurationSeconds: observation.DurationSeconds, Codec: observation.Codec, BitDepth: observation.BitDepth, SampleRate: observation.SampleRate, Channels: observation.Channels, Bitrate: observation.Bitrate, Artwork: observation.Artwork, ArtworkMIME: observation.ArtworkMIME}
	if organized.Directory == "" && organized.InferredFields["album"] {
		organized.Album = ""
	}
	return persistTestObservations(ctx, service, fixture, scanID, root, organized)
}
