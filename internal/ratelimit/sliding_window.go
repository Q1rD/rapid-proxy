package ratelimit

import (
	"sync"
	"time"
)

// SlidingWindow implements sliding window counter for metrics
// Used by circuit breaker to track error rates over time
type SlidingWindow struct {
	windowSize time.Duration // Size of the window
	bucketSize time.Duration // Size of each bucket

	mu      sync.RWMutex
	buckets map[int64]int64 // timestamp -> count
}

// NewSlidingWindow creates new sliding window
func NewSlidingWindow(windowSize, bucketSize time.Duration) *SlidingWindow {
	if bucketSize <= 0 {
		bucketSize = time.Second
	}
	if windowSize <= 0 {
		windowSize = 60 * time.Second
	}

	return &SlidingWindow{
		windowSize: windowSize,
		bucketSize: bucketSize,
		buckets:    make(map[int64]int64),
	}
}

// Increment increments counter for current time bucket
func (sw *SlidingWindow) Increment() {
	sw.IncrementBy(1)
}

// IncrementBy increments counter by specified amount
func (sw *SlidingWindow) IncrementBy(delta int64) {
	now := time.Now()
	bucketKey := sw.getBucketKey(now)

	sw.mu.Lock()
	defer sw.mu.Unlock()

	sw.buckets[bucketKey] += delta
	sw.cleanup(now)
}

// Count returns total count in the window
func (sw *SlidingWindow) Count() int64 {
	return sw.CountSince(time.Now().Add(-sw.windowSize))
}

// CountSince returns count since specified time
func (sw *SlidingWindow) CountSince(since time.Time) int64 {
	now := time.Now()

	sw.mu.RLock()
	defer sw.mu.RUnlock()

	var total int64
	for bucketTime, count := range sw.buckets {
		bucketTimestamp := time.Unix(0, bucketTime*int64(sw.bucketSize))
		if bucketTimestamp.After(since) && bucketTimestamp.Before(now) {
			total += count
		}
	}

	return total
}

// Rate returns average rate (count per second) in the window
func (sw *SlidingWindow) Rate() float64 {
	count := sw.Count()
	seconds := sw.windowSize.Seconds()
	if seconds == 0 {
		return 0
	}
	return float64(count) / seconds
}

// Reset clears all buckets
func (sw *SlidingWindow) Reset() {
	sw.mu.Lock()
	defer sw.mu.Unlock()

	sw.buckets = make(map[int64]int64)
}

// getBucketKey returns bucket key for given time
func (sw *SlidingWindow) getBucketKey(t time.Time) int64 {
	return t.UnixNano() / int64(sw.bucketSize)
}

// cleanup removes old buckets outside the window
// Must be called with lock held
func (sw *SlidingWindow) cleanup(now time.Time) {
	cutoff := now.Add(-sw.windowSize)
	cutoffKey := sw.getBucketKey(cutoff)

	for bucketKey := range sw.buckets {
		if bucketKey < cutoffKey {
			delete(sw.buckets, bucketKey)
		}
	}
}

// Stats returns sliding window statistics
func (sw *SlidingWindow) Stats() SlidingWindowStats {
	count := sw.Count()
	rate := sw.Rate()

	sw.mu.RLock()
	numBuckets := len(sw.buckets)
	sw.mu.RUnlock()

	return SlidingWindowStats{
		WindowSize: sw.windowSize,
		BucketSize: sw.bucketSize,
		Count:      count,
		Rate:       rate,
		NumBuckets: numBuckets,
	}
}

// SlidingWindowStats contains sliding window statistics
type SlidingWindowStats struct {
	WindowSize time.Duration
	BucketSize time.Duration
	Count      int64
	Rate       float64
	NumBuckets int
}
