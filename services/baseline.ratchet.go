package services

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/AxeForging/dupehound/domain"
)

// baselineVersion is bumped when the file format or fingerprint algorithm
// changes incompatibly; loading a newer version fails loudly instead of
// silently ratcheting against garbage.
const baselineVersion = 1

// BaselineFile is the on-disk format of a duplication baseline: the accepted
// debt at the moment --write-baseline ran. Fingerprints maps each clone's
// stable content hash to the number of instances it had. A later scan flags a
// clone as NEW when its fingerprint is absent — or present with MORE
// instances, so pasting yet another copy of known-duplicated code still fails.
//
// Fingerprints are content-based (normalized token structure), so the
// baseline survives line shifts, file renames, and unrelated edits without
// churn. The file is deterministic (sorted keys) and diff-friendly, meant to
// be committed next to .dupehound.yml.
type BaselineFile struct {
	Version      int            `json:"version"`
	MinTokens    int            `json:"min_tokens,omitempty"` // informative: threshold the baseline was built with
	Fingerprints map[string]int `json:"fingerprints"`         // clone hash → instance count at baseline time
}

// WriteBaseline records the given clones as accepted debt at path.
// Suppressed clones are excluded — they are already permanently ignored via
// .dupehound-ignore and would only bloat the file.
func WriteBaseline(path string, clones []domain.Clone, minTokens int) error {
	fps := make(map[string]int, len(clones))
	for _, c := range clones {
		if c.Suppressed {
			continue
		}
		if n := len(c.Instances); n > fps[c.Hash] {
			fps[c.Hash] = n
		}
	}
	b := BaselineFile{Version: baselineVersion, MinTokens: minTokens, Fingerprints: fps}
	data, err := json.MarshalIndent(b, "", "  ") // map keys marshal sorted → deterministic, diffable file
	if err != nil {
		return fmt.Errorf("marshal baseline: %w", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write baseline %s: %w", path, err)
	}
	return nil
}

// LoadBaseline reads and validates a baseline file written by WriteBaseline.
func LoadBaseline(path string) (*BaselineFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read baseline %s: %w", path, err)
	}
	var b BaselineFile
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, fmt.Errorf("parse baseline %s: %w", path, err)
	}
	if b.Version != baselineVersion {
		return nil, fmt.Errorf("baseline %s has version %d, this dupehound expects %d — regenerate with --write-baseline", path, b.Version, baselineVersion)
	}
	if b.Fingerprints == nil {
		b.Fingerprints = map[string]int{}
	}
	return &b, nil
}

// ApplyBaseline marks clones covered by the baseline (known debt) and returns
// how many are known vs new. A clone is known when its fingerprint is recorded
// and its instance count has not grown beyond the recorded count.
func ApplyBaseline(clones []domain.Clone, b *BaselineFile) (known, newCount int) {
	for i := range clones {
		if n, ok := b.Fingerprints[clones[i].Hash]; ok && len(clones[i].Instances) <= n {
			clones[i].Baseline = true
			known++
		} else {
			newCount++
		}
	}
	return known, newCount
}
