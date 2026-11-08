package parser

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
)

// creditTagRoles 只接受已约定的署名键，不把其它标签内容误当署名。
func creditTagRoles(key string) []metadata.RoleAssignment {
	key = strings.TrimPrefix(strings.TrimPrefix(strings.ToUpper(strings.TrimSpace(key)), "TXXX:"), "----:")
	switch key {
	case "TCOM", "©WRT":
		key = "COMPOSER"
	case "TEXT":
		key = "LYRICIST"
	case "TPE3":
		key = "CONDUCTOR"
	case "PERFORMERS":
		key = "PERFORMER"
	case "SOLOISTS":
		key = "SOLOIST"
	case "MIXARTIST":
		key = "REMIXER"
	case "ENSEMBLE", "ORCHESTRA":
		return metadata.MapInstrument(key)
	case "COMPOSER", "LYRICIST", "WRITER", "ARRANGER", "CONDUCTOR", "PRODUCER", "MIXER", "ENGINEER", "REMIXER", "DJMIXER", "PERFORMER", "SOLOIST", "LIBRETTIST":
	default:
		return nil
	}
	return metadata.MapCreditRole(key)
}

var performerCreditPattern = regexp.MustCompile(`^(.+?)\s*[(（]([^()（）]+)[)）]\s*$`)
var instrumentSeparators = regexp.MustCompile(`,|、| & | and `)

func addTagCredits(observation *AudioObservation, key, raw string) {
	for _, assignment := range creditTagRoles(key) {
		if assignment.Role == "performer" && assignment.Detail == "" {
			addPerformerCredits(observation, raw)
		} else {
			addAssignedCredits(observation, assignment, raw, "tag")
		}
	}
}

func addPerformerCredits(observation *AudioObservation, raw string) {
	for _, value := range metadata.SplitExplicitArtistValues(raw) {
		addPerformerCredit(observation, value)
	}
}

func addPerformerCredit(observation *AudioObservation, raw string) {
	parts := performerCreditPattern.FindStringSubmatch(strings.TrimSpace(raw))
	if len(parts) != 3 {
		addAssignedCredits(observation, metadata.RoleAssignment{Role: "performer"}, raw, "tag")
		return
	}
	var assignments []metadata.RoleAssignment
	for _, part := range instrumentSeparators.Split(parts[2], -1) {
		part = strings.TrimSpace(part)
		mapped := metadata.MapInstrument(part)
		if len(mapped) == 0 {
			mapped = metadata.MapCreditRole(part)
		}
		if len(mapped) == 0 {
			if part == "" || strings.IndexFunc(part, func(r rune) bool {
				return !unicode.Is(unicode.Latin, r) && r != ' ' && r != '-'
			}) >= 0 {
				// 任一未知非拉丁片段都保留整串，避免把真实括号别名当作乐器。
				appendObservationCredit(observation, metadata.Credit{Role: "performer", Name: metadata.BoundedText(raw), Source: "tag"})
				return
			}
			mapped = []metadata.RoleAssignment{{Role: "performer", Detail: metadata.BoundedCreditDetail(part)}}
		}
		assignments = append(assignments, mapped...)
	}
	for _, assignment := range assignments {
		addAssignedCredits(observation, assignment, parts[1], "tag")
	}
}

func addAssignedCredits(observation *AudioObservation, assignment metadata.RoleAssignment, raw, source string) {
	for _, name := range splitCreditNames(raw) {
		credit := metadata.Credit{Role: assignment.Role, Name: name, Detail: metadata.BoundedCreditDetail(assignment.Detail), Source: source}
		appendObservationCredit(observation, credit)
	}
}

func appendObservationCredit(observation *AudioObservation, credit metadata.Credit) {
	if credit.Name == "" {
		return
	}
	for _, previous := range observation.Credits {
		if previous == credit {
			return
		}
	}
	observation.Credits = append(observation.Credits, credit)
}

func addID3PairedCredits(observation *AudioObservation, frameID string, payload []byte) error {
	values := decodeID3ArtistValues(payload)
	for i := 0; i+1 < len(values); i += 2 {
		job, raw := values[i], values[i+1]
		assignments := metadata.MapCreditRole(job)
		fallbackRole := "producer"
		if frameID == "TMCL" {
			assignments = metadata.MapInstrument(job)
			fallbackRole = "performer"
		}
		if len(assignments) == 0 {
			assignments = []metadata.RoleAssignment{{Role: fallbackRole, Detail: metadata.BoundedCreditDetail(job)}}
		}
		key := frameID + ":" + job
		if len(key) > 128 {
			end := 128
			for !utf8.RuneStart(key[end]) {
				end--
			}
			key = key[:end]
		}
		if err := captureArtistTagRole(observation, assignments[0].Role, key, raw); err != nil {
			return err
		}
		for _, assignment := range assignments {
			addAssignedCredits(observation, assignment, raw, "tag")
		}
	}
	return nil
}
