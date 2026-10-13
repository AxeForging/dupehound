package services

import (
	"fmt"
	"math"
	"runtime/debug"
)

// Memory accounting for a scan.
//
// The budget is enforced against an estimate computed from what is being held
// (source bytes, token count, window count), not against live heap readings.
// An estimate is the same on every run of the same input, so whether a scan
// fits — and whether type-3 detection runs — does not depend on garbage
// collector timing or on what else the machine is doing.

const (
	// bytesPerToken is what one lexed token holds through exact detection:
	// the Token itself, its two mask bytes, its window hash, the transient
	// sorted copy of that hash, and its coverage flag.
	bytesPerToken = 48 + 2 + 8 + 8 + 1

	// heapHeadroom scales what the scan holds up to what the process occupies.
	// Go's collector lets the heap grow to about twice the live data before it
	// runs, and the report (clone previews, encoded output) comes on top.
	// Measured peak RSS over held bytes was 2.6–3.3× across the seven
	// evaluation repositories; this rounds up.
	heapHeadroom = 3.5

	// fuzzyBytesPerMiniHash is what one mini-window hash of one candidate
	// window costs in the type-3 index: its slot in the block's set and its
	// share of the hash → blocks map. The window count is an upper bound
	// (windows already explained by an exact clone are skipped), so this is
	// set from measurement rather than from the struct sizes: the lowest value
	// that still covered the measured index on every evaluation repository.
	fuzzyBytesPerMiniHash = 30
)

// memoryBudget tracks the estimated memory of a scan against a limit.
type memoryBudget struct {
	limit     int64 // 0 = no budget
	estimate  int64
	prevLimit int64
}

// newMemoryBudget starts accounting against limit and tells the Go runtime to
// collect garbage hard enough to stay under it.
func newMemoryBudget(limit int64) *memoryBudget {
	b := &memoryBudget{limit: limit, prevLimit: -1}
	if limit > 0 {
		b.prevLimit = debug.SetMemoryLimit(limit)
	}
	return b
}

// release restores the runtime limit that was in place before the scan.
func (b *memoryBudget) release() {
	if b.prevLimit >= 0 {
		debug.SetMemoryLimit(b.prevLimit)
	}
}

// add accounts for one tokenized file.
func (b *memoryBudget) add(sourceBytes, tokens int) {
	b.estimate += int64(heapHeadroom * float64(int64(sourceBytes)+int64(tokens)*bytesPerToken))
}

func (b *memoryBudget) exceeded() bool {
	return b.limit > 0 && b.estimate > b.limit
}

// remaining is what is left for type-3 detection: 0 when there is no budget,
// otherwise at least 1 so that "nothing left" is not mistaken for "no limit".
func (b *memoryBudget) remaining() int64 {
	if b.limit <= 0 {
		return 0
	}
	return max(b.limit-b.estimate, 1)
}

// fuzzyIndexBytes estimates the memory type-3 detection needs for the given
// number of candidate windows.
func fuzzyIndexBytes(windows, minTokens int) int64 {
	miniSize := max(int(math.Ceil(float64(minTokens)/3)), 2)
	perBlock := int64(64 + (minTokens-miniSize+1)*fuzzyBytesPerMiniHash)
	return int64(windows) * perBlock
}

// formatBytes renders a byte count for a log line or error message.
func formatBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GiB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.0f MiB", float64(n)/(1<<20))
	default:
		return fmt.Sprintf("%d KiB", n>>10)
	}
}
