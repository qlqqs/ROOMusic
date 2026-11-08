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
	case "TEXT", "AUTHOR", "IWRI":
		key = "LYRICIST"
	case "TPE3", "©CON":
		key = "CONDUCTOR"
	case "PERFORMERS", "©PRF":
		key = "PERFORMER"
	case "SOLOISTS":
		key = "SOLOIST"
	case "MIXARTIST", "REMIXED BY":
		key = "REMIXER"
	case "IPRO":
		key = "PRODUCER"
	case "ENSEMBLE", "ORCHESTRA":
		return metadata.MapInstrument(key)
	case "COMPOSER", "LYRICIST", "WRITER", "ARRANGER", "CONDUCTOR", "PRODUCER", "MIXER", "ENGINEER", "REMIXER", "DJMIXER", "PERFORMER", "SOLOIST", "LIBRETTIST", "LIBRETTO", "VOCALS", "VOCALIST":
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

// personnelTagKind 只识别人员表入口，不将工种别名加入全局角色词表。
func personnelTagKind(key string) string {
	key = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(strings.ToUpper(strings.TrimSpace(key)), "TXXX:"), "----:"))
	switch key {
	case "PERSONNEL", "INVOLVEDPEOPLE", "TIPL", "IPLS", "TMCL":
		return key
	default:
		return ""
	}
}

var involvedPeoplePattern = regexp.MustCompile(`([^(]+)\(([^)]+)\)`)

type personnelPair struct {
	name string
	job  string
}

func addID3PairedCredits(observation *AudioObservation, frameID string, payload []byte) error {
	values := decodeID3ArtistValues(payload)
	if len(values) > 1 {
		return addNULPairedCredits(observation, frameID, values)
	}
	for _, raw := range values {
		if err := addPersonnelCredits(observation, frameID, raw); err != nil {
			return err
		}
	}
	return nil
}

func addNULPairedCredits(observation *AudioObservation, key string, values []string) error {
	// 保持原有 NUL 成对规则：奇数尾项丢弃，原文不预先 trim。
	for i := 0; i+1 < len(values); i += 2 {
		if err := addPersonnelPair(observation, key, values[i], values[i+1]); err != nil {
			return err
		}
	}
	return nil
}

func addPersonnelCredits(observation *AudioObservation, key, raw string) error {
	kind := personnelTagKind(key)
	if kind != "PERSONNEL" && strings.Contains(raw, "\x00") {
		return addNULPairedCredits(observation, key, strings.Split(strings.TrimRight(raw, "\x00"), "\x00"))
	}
	for _, line := range strings.Split(raw, "\n") {
		var pairs []personnelPair
		if kind == "PERSONNEL" {
			for _, separator := range []string{"\t", "  ", " - "} {
				if name, job, found := strings.Cut(line, separator); found {
					pairs = []personnelPair{{name: strings.TrimSpace(name), job: strings.TrimSpace(job)}}
					break
				}
			}
		} else {
			pairs = parseInvolvedPeopleLine(line)
		}
		for _, pair := range pairs {
			if kind == "PERSONNEL" && (pair.job == "" || pair.name == "") {
				continue
			}
			if err := addPersonnelPair(observation, key, pair.job, pair.name); err != nil {
				return err
			}
		}
	}
	return nil
}

func parseInvolvedPeopleLine(line string) []personnelPair {
	// 仅括号形态按“名字、工种”原顺序；不展开方括号修饰语义。
	if parts := involvedPeoplePattern.FindStringSubmatch(line); len(parts) == 3 {
		return []personnelPair{{name: parts[1], job: parts[2]}}
	}
	if parts := strings.Split(line, "0x00"); len(parts)%2 == 0 {
		return alternatingPersonnelPairs(parts)
	}
	parts := strings.Split(line, ";")
	pairs := make([]personnelPair, 0, len(parts))
	for _, part := range parts {
		job, name, found := strings.Cut(part, ":")
		if !found {
			pairs = nil
			break
		}
		pairs = append(pairs, personnelPair{name: strings.TrimSpace(name), job: strings.TrimSpace(job)})
	}
	if len(pairs) > 0 {
		return pairs
	}
	if len(parts)%2 == 0 {
		return alternatingPersonnelPairs(parts)
	}
	return nil
}

func alternatingPersonnelPairs(parts []string) []personnelPair {
	pairs := make([]personnelPair, 0, len(parts)/2)
	for i := 0; i+1 < len(parts); i += 2 {
		pairs = append(pairs, personnelPair{name: strings.TrimSpace(parts[i+1]), job: strings.TrimSpace(parts[i])})
	}
	return pairs
}

func addPersonnelPair(observation *AudioObservation, key, job, raw string) error {
	assignments := metadata.MapCreditRole(job)
	fallbackRole := "producer"
	switch personnelTagKind(key) {
	case "TMCL":
		assignments = metadata.MapInstrument(job)
		fallbackRole = "performer"
	case "PERSONNEL":
		if len(assignments) == 0 {
			assignments = metadata.MapInstrument(job)
		}
		fallbackRole = "performer"
	}
	if len(assignments) == 0 {
		assignments = []metadata.RoleAssignment{{Role: fallbackRole, Detail: metadata.BoundedCreditDetail(job)}}
	}
	key += ":" + job
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
	return nil
}
