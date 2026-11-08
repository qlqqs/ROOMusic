package ingest

import (
	"strings"

	"github.com/qlqq/roomusic/backend/internal/catalog/classical"
	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
)

// WorkAssignment 是本轮扫描写入的作品事实；零序号写成 NULL，不进入专辑页作品分组。
type WorkAssignment struct {
	Title          string
	Catalog        string
	Index          int
	MovementNumber int
	MovementTitle  string
	Source         string
}

// AssignWorks 只消费扫描候选，不读取人工流派修正，也不参与 organizer 的发行归组。
func AssignWorks(candidate OrganizedCandidate) map[string]WorkAssignment {
	tracks := candidate.orderedTracks()
	works := make([]WorkAssignment, len(tracks))
	inferredGroups := make([]int, len(tracks))
	classicalGenre := hasClassicalGenre(candidate.Genres)
	for i := 0; i < len(tracks); {
		observation := tracks[i].Observation
		if observation.Work != "" {
			movement := observation.MovementName
			if movement == "" {
				movement = movementFromTaggedTitle(observation.Title, observation.Work)
			}
			works[i] = WorkAssignment{Title: observation.Work, MovementTitle: movement, MovementNumber: observation.MovementNumber, Source: "tag"}
			i++
			continue
		}
		start := i
		var titles []string
		for i < len(tracks) && tracks[i].Observation.Work == "" {
			titles = append(titles, tracks[i].Observation.Title)
			i++
		}
		if classicalGenre {
			for j, split := range classical.SplitWorks(titles) {
				if split.Group > 0 {
					works[start+j] = WorkAssignment{Title: split.Work, MovementTitle: split.Movement, Source: "title"}
					inferredGroups[start+j] = split.Group
				}
			}
		}
	}

	result := make(map[string]WorkAssignment)
	index := 0
	for start := 0; start < len(works); {
		end := start + 1
		for end < len(works) && sameWork(works[end-1], works[end], inferredGroups[end-1], inferredGroups[end]) {
			end++
		}
		if works[start].Title != "" {
			if end-start > 1 {
				index++
			}
			for i := start; i < end; i++ {
				work := works[i]
				if end-start > 1 {
					work.Index = index
				} else if work.Source == "title" {
					// 推断出的单曲组不保存任何作品字段；标签作品仍可搜索。
					continue
				}
				if catalogs := classical.ParseCatalogs(work.Title); len(catalogs) > 0 {
					entry := catalogs[0]
					work.Catalog = strings.TrimSpace(string([]rune(work.Title)[entry.Start : entry.Start+entry.Length]))
				}
				result[NormalizedPath(tracks[i].Observation.RelativePath)] = work
			}
		}
		start = end
	}
	return result
}

func hasClassicalGenre(genres []string) bool {
	for _, genre := range genres {
		key := metadata.GenreKey(genre)
		switch key {
		case "classical", "classic", "classique", "klassik", "clásica", "clasica", "classica", "クラシック", "클래식":
			return true
		}
		if strings.Contains(key, "古典") {
			return true
		}
	}
	return false
}

func movementFromTaggedTitle(title, work string) string {
	for _, separator := range []string{": ", "：", " - ", ", ", "，"} {
		if movement, found := strings.CutPrefix(title, work+separator); found {
			return strings.TrimSpace(movement)
		}
	}
	return ""
}

func sameWork(previous, current WorkAssignment, previousGroup, currentGroup int) bool {
	if previous.Title == "" || current.Title == "" || previous.Source != current.Source || workNameKey(previous.Title) != workNameKey(current.Title) {
		return false
	}
	if current.Source == "title" {
		return previousGroup == currentGroup
	}
	if previous.MovementNumber > 0 && current.MovementNumber > 0 && current.MovementNumber <= previous.MovementNumber {
		return false
	}
	movement := workNameKey(current.MovementTitle)
	return movement == "" || movement == "promenade" || movement != workNameKey(previous.MovementTitle)
}

func workNameKey(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
