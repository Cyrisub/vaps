package httpapi

import "testing"

func TestPullTrackerRecordsLayeredHits(t *testing.T) {
	tracker := newPullTracker()
	tracker.recordDiskHit(true)
	tracker.recordMemoryHit()
	tracker.recordRemote(true)

	stats := tracker.snapshot()
	if stats.Hits != 2 || stats.Misses != 1 {
		t.Fatalf("overall = %d hits / %d misses, want 2 / 1", stats.Hits, stats.Misses)
	}
	if stats.Memory.Hits != 1 || stats.Memory.Misses != 2 {
		t.Fatalf("memory = %#v, want 1 hit / 2 misses", stats.Memory)
	}
	if stats.Disk.Hits != 1 || stats.Disk.Misses != 1 {
		t.Fatalf("disk = %#v, want 1 hit / 1 miss", stats.Disk)
	}
}

func TestPullTrackerSkipsMemoryWhenUnchecked(t *testing.T) {
	tracker := newPullTracker()
	tracker.recordDiskHit(false)
	tracker.recordRemote(false)

	stats := tracker.snapshot()
	if stats.Hits != 1 || stats.Misses != 1 {
		t.Fatalf("overall = %d hits / %d misses, want 1 / 1", stats.Hits, stats.Misses)
	}
	if stats.Memory != (hitMissStats{}) {
		t.Fatalf("memory = %#v, want zero", stats.Memory)
	}
	if stats.Disk.Hits != 1 || stats.Disk.Misses != 1 {
		t.Fatalf("disk = %#v, want 1 hit / 1 miss", stats.Disk)
	}
}

func TestNilPullTrackerIsSafe(t *testing.T) {
	var tracker *pullTracker
	tracker.recordMemoryHit()
	tracker.recordDiskHit(true)
	tracker.recordRemote(false)
	if stats := tracker.snapshot(); stats != (pullStats{}) {
		t.Fatalf("nil tracker snapshot = %#v", stats)
	}
}
