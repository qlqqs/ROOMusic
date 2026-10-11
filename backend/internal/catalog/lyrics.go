package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// 歌词的显示形态与来源：synced 的每行带开始时间，plain 的每行没有；source 目前只有内嵌标签一种。
const (
	LyricsKindSynced     = "synced"
	LyricsKindPlain      = "plain"
	LyricsSourceEmbedded = "embedded"
)

const (
	maxLyricsLines     = 2000
	maxLyricsLineRunes = 1000
	// 解析后的开始时间超过 24 小时的行丢弃：不规范的时间戳不应撑出一首歌不可能到达的位置。
	maxLyricsStartMS = int64(24 * 60 * 60 * 1000)
)

// LyricsLine 是一行歌词。StartMS 在 synced 歌词里是非负毫秒数，在 plain 歌词里为 nil（JSON null）；Text 可以是空串（间奏或分段）。
type LyricsLine struct {
	StartMS *int64 `json:"start_ms"`
	Text    string `json:"text"`
}

// Lyrics 是解析后的歌词。synced 的行按 StartMS 非递减排列；时间相同的行保持原顺序（双语对照靠它分组）。
type Lyrics struct {
	Kind   string       `json:"kind"`
	Source string       `json:"source"`
	Lines  []LyricsLine `json:"lines"`
}

// TrackLyrics 是歌词接口的响应：曲目没有可显示的歌词时 Lyrics 为 nil（JSON null）。
type TrackLyrics struct {
	TrackID string  `json:"track_id"`
	Lyrics  *Lyrics `json:"lyrics"`
}

// LoadTrackLyrics 读取 present 曲目的内嵌歌词并解析；曲目不存在或已缺失时返回 ErrTrackNotFound。
// 它只读数据库，不触碰文件系统，也不检查目录状态（歌词是已入库的目录事实，停用目录后仍可浏览）。
// trackID 必须是 UUID 文本，由调用方在传输层校验。
func (s *Store) LoadTrackLyrics(ctx context.Context, trackID string) (TrackLyrics, error) {
	var id string
	var raw sql.NullString
	err := s.database.QueryRowContext(ctx, `SELECT id::text, lyrics FROM tracks WHERE id=$1::uuid AND source_status='present'`, trackID).Scan(&id, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return TrackLyrics{}, ErrTrackNotFound
	}
	if err != nil {
		return TrackLyrics{}, fmt.Errorf("load track lyrics: %w", err)
	}
	result := TrackLyrics{TrackID: id}
	if raw.Valid {
		if lyrics, ok := ParseLyrics(raw.String); ok {
			result.Lyrics = &lyrics
		}
	}
	return result, nil
}

var (
	// 行首的时间戳：[mm:ss]、[mm:ss.f]、[mm:ss.ff]、[mm:ss.fff]，小数也可以用冒号写成 [mm:ss:ff]；秒是否小于 60 在代码里判断。
	lyricsTimestampPattern = regexp.MustCompile(`^\[(\d{1,3}):(\d{1,2})(?:[.:](\d{1,3}))?\]`)
	// 整行的元标签：[ti:标题]、[offset:+500] 等，键由字母或 # 组成。
	lyricsMetaPattern = regexp.MustCompile(`^\[([A-Za-z#]+):([^\]]*)\]$`)
	// 逐字标记 <mm:ss.xx>：只显示整行，所以去掉。
	lyricsWordMarkPattern = regexp.MustCompile(`<\d{1,3}:\d{1,2}(?:[.:]\d{1,3})?>`)
)

// ParseLyrics 把保存的歌词原文解析成可显示的行。至少一行有合法的行首时间戳时是 synced，否则是 plain；
// 去掉元标签后没有任何可显示的行时返回 false（等同没有歌词）。它是纯函数，规则只在这里有一份，前端只做分组与定位。
//
// synced：每个行首时间戳生成一行，文本是去掉时间戳与逐字标记后的剩余部分（去首尾空白，可为空串）；
// 开始时间 = 时间戳 − [offset:] 的毫秒值（第一个有效的 offset 生效），小于 0 取 0，超过 24 小时的行丢弃；
// 没有时间戳的行丢弃；按开始时间稳定排序。过滤后一行不剩时按 plain 处理。
// plain：去掉元标签行，行尾去空白，去掉首尾空行，连续空行合并为一行（行首的合法时间戳也一并去掉，所以可读）。
// 两种形态最多 2,000 行，单行最多 1,000 个字符（超出截断）。
func ParseLyrics(raw string) (Lyrics, bool) {
	type entry struct {
		start int64
		text  string
	}
	var entries []entry
	var plainLines []string
	var offset int64
	offsetSet := false
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimRight(line, "\r")
		trimmed := strings.TrimSpace(line)
		if match := lyricsMetaPattern.FindStringSubmatch(trimmed); match != nil {
			if !offsetSet && strings.EqualFold(match[1], "offset") {
				if value, ok := parseLyricsOffset(match[2]); ok {
					offset, offsetSet = value, true
				}
			}
			continue
		}
		starts, rest := splitLyricsTimestamps(trimmed)
		if len(starts) == 0 {
			plainLines = append(plainLines, strings.TrimRight(line, " \t"))
			continue
		}
		plainLines = append(plainLines, strings.TrimSpace(rest))
		text := strings.TrimSpace(lyricsWordMarkPattern.ReplaceAllString(rest, ""))
		for _, start := range starts {
			entries = append(entries, entry{start: start, text: text})
		}
	}

	if len(entries) > 0 {
		lines := make([]LyricsLine, 0, len(entries))
		for _, item := range entries {
			start := max(item.start-offset, 0)
			if start > maxLyricsStartMS {
				continue
			}
			lines = append(lines, LyricsLine{StartMS: &start, Text: truncateLyricsLine(item.text)})
		}
		if len(lines) > 0 {
			sort.SliceStable(lines, func(i, j int) bool { return *lines[i].StartMS < *lines[j].StartMS })
			if len(lines) > maxLyricsLines {
				lines = lines[:maxLyricsLines]
			}
			return Lyrics{Kind: LyricsKindSynced, Source: LyricsSourceEmbedded, Lines: lines}, true
		}
	}

	lines := make([]LyricsLine, 0, len(plainLines))
	previousBlank := true // 开头的空行与连续空行都不保留
	for _, text := range plainLines {
		blank := text == ""
		if blank && previousBlank {
			continue
		}
		previousBlank = blank
		lines = append(lines, LyricsLine{Text: truncateLyricsLine(text)})
	}
	if len(lines) > maxLyricsLines {
		lines = lines[:maxLyricsLines]
	}
	for len(lines) > 0 && lines[len(lines)-1].Text == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return Lyrics{}, false
	}
	return Lyrics{Kind: LyricsKindPlain, Source: LyricsSourceEmbedded, Lines: lines}, true
}

// splitLyricsTimestamps 取出行首连续的合法时间戳（毫秒）与其后的剩余文本；遇到秒不小于 60 的标签就停止，该标签留在文本里。
func splitLyricsTimestamps(line string) (starts []int64, rest string) {
	rest = line
	for {
		match := lyricsTimestampPattern.FindStringSubmatch(rest)
		if match == nil {
			return starts, rest
		}
		minutes, _ := strconv.Atoi(match[1])
		seconds, _ := strconv.Atoi(match[2])
		if seconds >= 60 {
			return starts, rest
		}
		milliseconds := 0
		if match[3] != "" {
			// 一位是十分之一秒，两位是百分之一秒，三位是毫秒：右侧补零到三位。
			milliseconds, _ = strconv.Atoi((match[3] + "00")[:3])
		}
		starts = append(starts, int64(minutes*60+seconds)*1000+int64(milliseconds))
		rest = rest[len(match[0]):]
	}
}

// parseLyricsOffset 解析 [offset:] 的整数毫秒值（可带正负号）；不是整数或超过 24 小时时视为无效。
func parseLyricsOffset(value string) (int64, bool) {
	parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || parsed > maxLyricsStartMS || parsed < -maxLyricsStartMS {
		return 0, false
	}
	return parsed, true
}

// truncateLyricsLine 把一行截到 1,000 个字符以内。
func truncateLyricsLine(text string) string {
	if len(text) <= maxLyricsLineRunes {
		return text
	}
	count := 0
	for index := range text {
		if count == maxLyricsLineRunes {
			return text[:index]
		}
		count++
	}
	return text
}
