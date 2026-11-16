package runtime

import (
	"encoding/json"
	"strings"
	"unsafe"
)

// 文本按有界块累积，边界保留每个已发序号对应的字节位置。
// 同一步超过块上限后另起一块，使预算淘汰先释放旧文本，保留最新增量。
const textLogChunkBytes = 64 << 10
const textLogChunkBoundaries = 1024

const logEntryBytes = int(unsafe.Sizeof(logEntry{}))
const textBoundaryBytes = int(unsafe.Sizeof(textBoundary{}))
const textBuilderBytes = int(unsafe.Sizeof(strings.Builder{}))

type textBoundary struct {
	seq uint64
	end int
}
type logEntry struct {
	event      Event
	text       *strings.Builder
	boundaries []textBoundary
	size       int
}
type eventLog struct {
	entries []logEntry
	bytes   int
	storage int // 条目数组的实际分配容量；移除前缀不会立即释放其底层数组。
	omitted uint64
}

func (l *eventLog) append(event Event) {
	n := len(l.entries)
	if event.Type == EventText && n > 0 && l.entries[n-1].event.Type == EventText && l.entries[n-1].event.Step == event.Step &&
		l.entries[n-1].text.Len()+len(event.Text) <= textLogChunkBytes && len(l.entries[n-1].boundaries) < textLogChunkBoundaries {
		entry := &l.entries[n-1]
		oldSize := entry.size
		entry.appendText(event)
		l.bytes += entry.size - oldSize
	} else {
		entry := logEntry{event: event}
		if event.Type == EventText {
			entry.text = &strings.Builder{}
			entry.appendText(event)
		} else {
			// 非文本按序列化载荷估算其嵌套对象，条目本身另计。
			raw, _ := json.Marshal(event)
			entry.size = len(raw)
		}
		allocated := len(l.entries) == cap(l.entries)
		l.entries = append(l.entries, entry)
		if allocated {
			storage := cap(l.entries) * logEntryBytes
			l.bytes += storage - l.storage
			l.storage = storage
		}
		l.bytes += entry.size
	}
	// 连工具载荷也计入上限；省略前缀用一个游标表示，客户端收到后重新读取详情。
	for l.bytes > EventLogLimit-logEntryBytes && len(l.entries) > 0 {
		old := l.entries[0]
		l.omitted = old.event.Seq
		l.bytes -= old.size
		l.entries[0] = logEntry{}
		l.entries = l.entries[1:]
		if len(l.entries) == 0 {
			l.entries = nil
			l.bytes -= l.storage
			l.storage = 0
		}
	}
}

func (entry *logEntry) appendText(event Event) {
	entry.text.WriteString(event.Text)
	entry.event.Text = entry.text.String()
	entry.event.Seq = event.Seq
	entry.boundaries = append(entry.boundaries, textBoundary{event.Seq, entry.text.Len()})
	entry.size = textBuilderBytes + entry.text.Cap() + cap(entry.boundaries)*textBoundaryBytes
}

func (l *eventLog) after(seq uint64) []Event {
	result := []Event{}
	if l.omitted > seq {
		result = append(result, Event{Type: EventHistoryGap, Seq: l.omitted})
	}
	for _, entry := range l.entries {
		if entry.event.Seq <= seq {
			continue
		}
		event := entry.event
		if event.Type == EventText {
			offset := 0
			for _, boundary := range entry.boundaries {
				if boundary.seq <= seq {
					offset = boundary.end
				} else {
					break
				}
			}
			event.Text = strings.Clone(event.Text[offset:])
		}
		result = append(result, event)
	}
	return result
}
