package zones

import (
	"io"
	"sync"
	"sync/atomic"
	"time"
)

const groupBlockSize = 32 << 10
const groupConnectTimeout = 8 * time.Second

// 逻辑字节位置单调递增。环按需分块，最多同时保留一份尚有读者需要的音频。
type groupDispatcher struct {
	mu                sync.Mutex
	ready             *sync.Cond
	chunks            []groupChunk
	limit, first, end int64
	readers           []*groupReader
	terminal          error
	closed            bool
	drops             chan string
}

type groupChunk struct {
	data     []byte
	retained int
}
type groupReader struct {
	dispatch       *groupDispatcher
	id             string
	pos            int64
	taken, dropped bool
	delivered      atomic.Bool
	done           chan struct{}
}

func newGroupDispatcher(ids []string, format StreamFormat) *groupDispatcher {
	bytesPerSecond := int64(format.SampleRate) * int64(format.Channels) * int64((format.BitDepth+7)/8)
	limit := max(8<<20, 30*bytesPerSecond)
	d := &groupDispatcher{limit: limit, chunks: make([]groupChunk, (limit+groupBlockSize-1)/groupBlockSize), drops: make(chan string, len(ids))}
	d.ready = sync.NewCond(&d.mu)
	for _, id := range ids {
		d.readers = append(d.readers, &groupReader{dispatch: d, id: id, done: make(chan struct{})})
	}
	return d
}

func (d *groupDispatcher) pump(source io.Reader) {
	buf := make([]byte, groupBlockSize)
	for {
		n, err := source.Read(buf)
		d.mu.Lock()
		if d.closed {
			d.mu.Unlock()
			return
		}
		if n > 0 && !d.appendLocked(buf[:n]) {
			d.ready.Broadcast()
			d.mu.Unlock()
			return
		}
		d.terminal = err
		d.ready.Broadcast()
		d.mu.Unlock()
		if err != nil {
			return
		}
	}
}

// 全部读者已经越过的块即时释放。同一物理块跨环尾使用时，通过保留字节数判断其生命周期。
func (d *groupDispatcher) chunkAt(pos int64) (chunk *groupChunk, offset, size int) {
	physical := pos % d.limit
	start := physical / groupBlockSize * groupBlockSize
	return &d.chunks[start/groupBlockSize], int(physical - start), int(min(groupBlockSize, d.limit-start))
}

func (d *groupDispatcher) trimLocked() {
	first := d.end
	for _, r := range d.readers {
		if !r.dropped {
			first = min(first, r.pos)
		}
	}
	for d.first < first {
		block, offset, size := d.chunkAt(d.first)
		n := int(min(int64(size-offset), first-d.first))
		block.retained -= n
		if block.retained == 0 {
			block.data = nil
		}
		d.first += int64(n)
	}
}

func (d *groupDispatcher) appendLocked(data []byte) bool {
	end := d.end + int64(len(data))
	live := false
	for _, r := range d.readers {
		if !r.dropped && end-r.pos > d.limit {
			r.dropLocked(true)
		}
		live = live || !r.dropped
	}
	d.trimLocked()
	if !live {
		return false
	}
	for len(data) > 0 {
		block, offset, size := d.chunkAt(d.end)
		if block.data == nil {
			block.data = make([]byte, size)
		}
		n := copy(block.data[offset:], data)
		block.retained += n
		d.end += int64(n)
		data = data[n:]
	}
	return true
}

func (r *groupReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	d := r.dispatch
	d.mu.Lock()
	defer d.mu.Unlock()
	for r.pos == d.end && !r.dropped && !d.closed && d.terminal == nil {
		d.ready.Wait()
	}
	if d.closed {
		return 0, io.EOF
	}
	if r.dropped {
		return 0, ErrStreamGone
	}
	if r.pos == d.end {
		return 0, d.terminal
	}
	block, offset, size := d.chunkAt(r.pos)
	available := min(int64(size-offset), d.end-r.pos)
	n := copy(p, block.data[offset:offset+int(available)])
	r.pos += int64(n)
	d.trimLocked()
	return n, nil
}

func (r *groupReader) dropLocked(report bool) {
	if r.dropped {
		return
	}
	r.dropped = true
	close(r.done)
	// 每台最多投递一次，队列按成员数分配。
	if report {
		r.dispatch.drops <- r.id
	}
}

func (d *groupDispatcher) expire() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return
	}
	for _, r := range d.readers {
		if !r.taken {
			r.dropLocked(true)
		}
	}
	d.trimLocked()
	d.ready.Broadcast()
}

func (d *groupDispatcher) close() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.closed = true
	for _, r := range d.readers {
		r.dropLocked(false)
	}
	d.chunks = nil
	d.ready.Broadcast()
}
