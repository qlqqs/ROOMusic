package parser

import (
	"strconv"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/catalog/classical"
	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
)

// workTags 保留各级首个非空值，读完全部标签后决定优先级，避免依赖容器内的键顺序。
type workTags struct {
	work, nativeWork                     string
	movementName, nativeMovementName     string
	movementNumber, nativeMovementNumber string
	movement                             string
	id3Grouping                          bool
}

func collectWorkTag(observation *AudioObservation, key, value string) bool {
	key = strings.ToUpper(key)
	key = strings.TrimPrefix(strings.TrimPrefix(key, "TXXX:"), "----:")
	var destination *string
	tags := &observation.workTags
	switch key {
	case "WORK":
		destination = &tags.work
	case "TIT1", "©WRK":
		destination = &tags.nativeWork
		tags.id3Grouping = key == "TIT1"
	case "MOVEMENTNAME":
		destination = &tags.movementName
	case "MVNM", "©MVN":
		destination = &tags.nativeMovementName
	case "MOVEMENTNUMBER":
		destination = &tags.movementNumber
	case "MVIN", "©MVI":
		destination = &tags.nativeMovementNumber
	case "MOVEMENT":
		destination = &tags.movement
	case "MOVEMENTTOTAL", "©MVC":
		// 总乐章数不进入观察与展示合同。
		return true
	default:
		return false
	}
	if *destination == "" {
		*destination = metadata.BoundedText(value)
	}
	return true
}

func finalizeWorkTags(observation *AudioObservation) {
	tags := observation.workTags
	work := tags.nativeWork
	if tags.id3Grouping && tags.nativeMovementName == "" && tags.nativeMovementNumber == "" && len(classical.ParseCatalogs(work)) == 0 {
		// 旧版 TIT1 是普通分组；没有乐章标签或作品编号时不能当作作品。
		work = ""
	}
	if work == "" {
		work = tags.work
	}
	observation.Work = work
	for _, name := range []string{tags.nativeMovementName, tags.movementName} {
		if name != "" {
			observation.MovementName = name
			break
		}
	}
	for _, number := range []string{tags.nativeMovementNumber, tags.movementNumber} {
		if number != "" {
			observation.MovementNumber, _ = parseMovementNumber(number)
			break
		}
	}
	if number, numeric := parseMovementNumber(tags.movement); numeric {
		if tags.nativeMovementNumber == "" && tags.movementNumber == "" {
			observation.MovementNumber = number
		}
	} else if observation.MovementName == "" {
		observation.MovementName = tags.movement
	}
}

// parseMovementNumber 区分纯数字/n/m 与乐章名；只保存可写入正整数列的序号。
func parseMovementNumber(value string) (number int, numeric bool) {
	parts := strings.Split(value, "/")
	if len(parts) > 2 {
		return 0, false
	}
	for _, part := range parts {
		if part == "" {
			return 0, false
		}
		for _, c := range part {
			if c < '0' || c > '9' {
				return 0, false
			}
		}
	}
	parsed, err := strconv.ParseInt(parts[0], 10, 32)
	if err == nil && parsed > 0 {
		return int(parsed), true
	}
	return 0, true
}
