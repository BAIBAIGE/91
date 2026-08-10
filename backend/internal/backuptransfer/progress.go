package backuptransfer

import (
	"sync"
	"time"
)

const (
	rateWindow      = 5 * time.Second
	rateIdleTimeout = 2 * time.Second
	rateSampleEvery = 200 * time.Millisecond
)

type rateSample struct {
	at    time.Time
	bytes int64
}

// streamProgress keeps high-frequency transfer telemetry out of durable job
// and receipt state. Checkpoints remain sparse while the admin API can still
// report smooth in-flight progress and a short-window application throughput.
type streamProgress struct {
	mu           sync.Mutex
	committed    int64
	active       map[int]int64
	wireBytes    int64
	samples      []rateSample
	lastSample   time.Time
	lastActivity time.Time
}

func newStreamProgress(committed int64, now time.Time) *streamProgress {
	if committed < 0 {
		committed = 0
	}
	return &streamProgress{
		committed:  committed,
		active:     make(map[int]int64),
		samples:    []rateSample{{at: now, bytes: 0}},
		lastSample: now,
	}
}

func (p *streamProgress) begin(index int) bool {
	if p == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, active := p.active[index]; active {
		return false
	}
	p.active[index] = 0
	return true
}

func (p *streamProgress) add(index int, bytes int64, now time.Time) {
	if p == nil || bytes <= 0 {
		return
	}
	p.mu.Lock()
	p.active[index] += bytes
	p.wireBytes += bytes
	p.lastActivity = now
	if now.Sub(p.lastSample) >= rateSampleEvery {
		p.appendSampleLocked(now)
	}
	p.mu.Unlock()
}

func (p *streamProgress) commit(index int, committed int64, now time.Time) {
	if p == nil {
		return
	}
	p.mu.Lock()
	delete(p.active, index)
	if committed > p.committed {
		p.committed = committed
	}
	p.appendSampleLocked(now)
	p.mu.Unlock()
}

func (p *streamProgress) abandon(index int) {
	if p == nil {
		return
	}
	p.mu.Lock()
	delete(p.active, index)
	p.mu.Unlock()
}

func (p *streamProgress) setCommitted(bytes int64) {
	if p == nil || bytes < 0 {
		return
	}
	p.mu.Lock()
	if bytes > p.committed {
		p.committed = bytes
	}
	p.mu.Unlock()
}

func (p *streamProgress) snapshot(now time.Time, size int64) (int64, int64, time.Time) {
	if p == nil {
		return 0, 0, time.Time{}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.appendSampleLocked(now)
	processed := p.committed
	for _, bytes := range p.active {
		processed += bytes
	}
	if processed > size {
		processed = size
	}
	if processed < 0 {
		processed = 0
	}
	if p.lastActivity.IsZero() || now.Sub(p.lastActivity) > rateIdleTimeout || len(p.samples) < 2 {
		return processed, 0, p.lastActivity
	}
	oldest := p.samples[0]
	elapsed := now.Sub(oldest.at)
	if elapsed <= 0 {
		return processed, 0, p.lastActivity
	}
	rate := int64(float64(p.wireBytes-oldest.bytes) / elapsed.Seconds())
	if rate < 0 {
		rate = 0
	}
	return processed, rate, p.lastActivity
}

func (p *streamProgress) appendSampleLocked(now time.Time) {
	if now.Before(p.lastSample) {
		now = p.lastSample
	}
	if now.Equal(p.lastSample) && len(p.samples) > 0 {
		p.samples[len(p.samples)-1].bytes = p.wireBytes
	} else {
		p.samples = append(p.samples, rateSample{at: now, bytes: p.wireBytes})
		p.lastSample = now
	}
	cutoff := now.Add(-rateWindow)
	first := 0
	for first+1 < len(p.samples) && p.samples[first+1].at.Before(cutoff) {
		first++
	}
	if first > 0 {
		p.samples = append([]rateSample(nil), p.samples[first:]...)
	}
}

func (m *Manager) replaceOutgoingProgress(id string, committed int64) *streamProgress {
	progress := newStreamProgress(committed, m.nowTime())
	m.mu.Lock()
	m.outgoingProgress[id] = progress
	m.mu.Unlock()
	return progress
}

func (m *Manager) clearOutgoingProgress(id string) {
	m.mu.Lock()
	delete(m.outgoingProgress, id)
	m.mu.Unlock()
}

func (m *Manager) incomingProgressFor(id string, committed int64) *streamProgress {
	m.mu.Lock()
	progress := m.incomingProgress[id]
	if progress == nil {
		progress = newStreamProgress(committed, m.nowTime())
		m.incomingProgress[id] = progress
	}
	m.mu.Unlock()
	return progress
}

func (m *Manager) clearIncomingProgress(id string) {
	m.mu.Lock()
	delete(m.incomingProgress, id)
	m.mu.Unlock()
}
