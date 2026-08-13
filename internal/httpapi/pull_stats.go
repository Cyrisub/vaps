package httpapi

import "sync/atomic"

type hitMissStats struct {
	Hits   int64 `json:"hits"`
	Misses int64 `json:"misses"`
}

type pullStats struct {
	Hits   int64        `json:"hits"`
	Misses int64        `json:"misses"`
	Memory hitMissStats `json:"memory"`
	Disk   hitMissStats `json:"disk"`
}

type pullHitCounters struct {
	hits   atomic.Int64
	misses atomic.Int64
}

func (c *pullHitCounters) snapshot() hitMissStats {
	return hitMissStats{Hits: c.hits.Load(), Misses: c.misses.Load()}
}

type pullTracker struct {
	overall pullHitCounters
	memory  pullHitCounters
	disk    pullHitCounters
}

func newPullTracker() *pullTracker {
	return &pullTracker{}
}

func (t *pullTracker) recordMemoryHit() {
	if t == nil {
		return
	}
	t.memory.hits.Add(1)
	t.overall.hits.Add(1)
}

func (t *pullTracker) recordDiskHit(memoryChecked bool) {
	if t == nil {
		return
	}
	if memoryChecked {
		t.memory.misses.Add(1)
	}
	t.disk.hits.Add(1)
	t.overall.hits.Add(1)
}

func (t *pullTracker) recordRemote(memoryChecked bool) {
	if t == nil {
		return
	}
	if memoryChecked {
		t.memory.misses.Add(1)
	}
	t.disk.misses.Add(1)
	t.overall.misses.Add(1)
}

func (t *pullTracker) snapshot() pullStats {
	if t == nil {
		return pullStats{}
	}
	overall := t.overall.snapshot()
	return pullStats{
		Hits:   overall.Hits,
		Misses: overall.Misses,
		Memory: t.memory.snapshot(),
		Disk:   t.disk.snapshot(),
	}
}
