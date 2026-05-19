package registry

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// indexFile is the directory listing of all known instances. It's a
// derived view: each entry can be reconstructed from <name>/state.json,
// but we keep this denormalised index so `localnet list` is O(1) per
// instance instead of O(n) directory scans.
const indexFileName = "index.json"

// IndexEntry is the small slice of state surfaced in `localnet list`.
type IndexEntry struct {
	Name          string `json:"name"`
	SpliceVersion string `json:"splice_version"`
	Status        Status `json:"status"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
}

// Index is the on-disk format. The wrapping struct (rather than a bare
// array) lets us add fields later without a schema break.
type Index struct {
	SchemaVersion int          `json:"schema_version"`
	Entries       []IndexEntry `json:"entries"`
}

func indexPath() string {
	return filepath.Join(Root(), indexFileName)
}

// ReadIndex loads the index file. Returns an empty Index (no error) when
// the file doesn't exist yet.
func ReadIndex() (*Index, error) {
	data, err := os.ReadFile(indexPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &Index{SchemaVersion: SchemaVersion, Entries: []IndexEntry{}}, nil
		}
		return nil, fmt.Errorf("read index: %w", err)
	}
	var idx Index
	if err := json.Unmarshal(data, &idx); err != nil {
		return nil, fmt.Errorf("parse index: %w", err)
	}
	if idx.SchemaVersion == 0 {
		idx.SchemaVersion = SchemaVersion
	}
	return &idx, nil
}

// indexAdd upserts an entry derived from s. The whole read-modify-write
// runs under withIndexLock so concurrent ups of *different* instances
// each see a consistent prior view.
func indexAdd(s *State) error {
	return withIndexLock(func() error {
		idx, err := ReadIndex()
		if err != nil {
			return err
		}

		now := time.Now().UTC().Format(time.RFC3339)
		entry := IndexEntry{
			Name:          s.Name,
			SpliceVersion: s.SpliceVersion,
			Status:        s.Status,
			CreatedAt:     s.CreatedAt,
			UpdatedAt:     now,
		}

		found := false
		for i := range idx.Entries {
			if idx.Entries[i].Name == s.Name {
				idx.Entries[i] = entry
				found = true
				break
			}
		}
		if !found {
			idx.Entries = append(idx.Entries, entry)
		}
		sort.Slice(idx.Entries, func(i, j int) bool {
			return idx.Entries[i].Name < idx.Entries[j].Name
		})

		data, err := json.MarshalIndent(idx, "", "  ")
		if err != nil {
			return err
		}
		return atomicWrite(indexPath(), data, 0o644)
	})
}

// indexRemove deletes the named entry. No-op if the index file is missing.
func indexRemove(name string) error {
	return withIndexLock(func() error {
		if _, statErr := os.Stat(indexPath()); statErr != nil {
			return nil
		}
		idx, err := ReadIndex()
		if err != nil {
			return err
		}
		kept := idx.Entries[:0]
		for _, e := range idx.Entries {
			if e.Name != name {
				kept = append(kept, e)
			}
		}
		idx.Entries = kept

		data, err := json.MarshalIndent(idx, "", "  ")
		if err != nil {
			return err
		}
		return atomicWrite(indexPath(), data, 0o644)
	})
}
