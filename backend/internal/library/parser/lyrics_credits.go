package parser

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
)

var lyricsCreditHan = regexp.MustCompile(`\p{Han}+`)
var lyricsCreditLatin = regexp.MustCompile(`[\p{Latin}][\p{Latin} -]*`)

// addLyricsCredits 只读取规范化歌词开头的连续署名块，标签署名按角色整体优先。
func addLyricsCredits(observation *AudioObservation) {
	tagRoles := make(map[string]bool)
	for _, credit := range observation.Credits {
		if credit.Source == "" || credit.Source == "tag" {
			tagRoles[credit.Role] = true
		}
	}
	for index, line := range strings.Split(observation.Lyrics, "\n") {
		if index >= 30 {
			break
		}
		line = strings.TrimSpace(line)
		for strings.HasPrefix(line, "[") {
			end := strings.IndexByte(line, ']')
			if end < 0 {
				break
			}
			line = strings.TrimSpace(line[end+1:])
		}
		if line == "" {
			continue
		}
		colon := strings.IndexAny(line, ":：")
		if colon < 0 || utf8.RuneCountInString(line[:colon]) < 1 || utf8.RuneCountInString(line[:colon]) > 16 {
			break
		}
		_, width := utf8.DecodeRuneInString(line[colon:])
		raw := strings.TrimSpace(line[colon+width:])
		var assignments []metadata.RoleAssignment
		for _, label := range strings.FieldsFunc(line[:colon], func(r rune) bool { return r == '/' || r == '、' || r == '&' }) {
			assignments = append(assignments, lyricsLabelRoles(strings.TrimSpace(label))...)
		}
		if len(assignments) == 0 {
			break
		}
		for _, name := range splitCreditNames(raw) {
			if at := strings.IndexByte(name, '@'); at >= 0 {
				if trimmed := strings.TrimSpace(name[:at]); trimmed != "" {
					name = trimmed
				}
			}
			for _, assignment := range assignments {
				if !tagRoles[assignment.Role] {
					observation.Credits = append(observation.Credits, metadata.Credit{Role: assignment.Role, Name: name, Detail: assignment.Detail, Source: "lyrics"})
				}
			}
		}
	}
}

func lyricsLabelRoles(label string) []metadata.RoleAssignment {
	if assignments := lyricsExactLabelRoles(label); len(assignments) > 0 {
		return assignments
	}
	for _, pattern := range []*regexp.Regexp{lyricsCreditHan, lyricsCreditLatin} {
		for _, fragment := range pattern.FindAllString(label, -1) {
			if assignments := lyricsExactLabelRoles(strings.TrimSpace(fragment)); len(assignments) > 0 {
				return assignments
			}
		}
	}
	return nil
}

func lyricsExactLabelRoles(label string) []metadata.RoleAssignment {
	assignments := metadata.MapCreditRole(label)
	if len(assignments) == 0 {
		assignments = metadata.MapInstrument(label)
	}
	for i := range assignments {
		if assignments[i].Detail != "" {
			assignments[i].Detail = metadata.BoundedCreditDetail(label)
		}
	}
	return assignments
}
