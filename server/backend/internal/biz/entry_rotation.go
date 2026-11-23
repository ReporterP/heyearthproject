package biz

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/lib/pq"

	"github.com/heyearth/backend/internal/db"
)

// entryRotationQuery moves a relay's rotation to $2 only while its current rotation (no row =
// 'all') is one of $5: a concurrent change (the owner's second tab, the activation of a new
// entry) makes it affect no row.
const entryRotationQuery = `
INSERT INTO entry_state (entry_id, rotation, rotation_reason, rotation_at, rotation_by)
SELECT $1, $2, $3, NOW(), $4
 WHERE COALESCE((SELECT rotation FROM entry_state WHERE entry_id = $1), 'all') = ANY($5)
ON CONFLICT (entry_id) DO UPDATE SET rotation = EXCLUDED.rotation, rotation_reason = EXCLUDED.rotation_reason,
       rotation_at = EXCLUDED.rotation_at, rotation_by = EXCLUDED.rotation_by, updated_at = NOW()
 WHERE entry_state.rotation = ANY($5)`

// entryRotationReasonMax caps the stored reason, in runes.
const entryRotationReasonMax = 200

// SetEntryRotation moves relay id (> 0) from one of from to to, with the owner's reason and who
// did it. ErrRotationChanged when the current rotation is not in from. The caller bumps
// subscription_version and writes the audit log.
func SetEntryRotation(ctx context.Context, id int, from []string, to, reason, by string) error {
	valid := []string{EntryRotationAll, EntryRotationTest, EntryRotationOff}
	if id <= 0 {
		return fmt.Errorf("entry rotation: entry %d is not a relay", id)
	}
	if !slices.Contains(valid, to) || len(from) == 0 || slices.ContainsFunc(from, func(s string) bool { return !slices.Contains(valid, s) }) {
		return fmt.Errorf("entry rotation: unknown rotation %q (from %v)", to, from)
	}
	reason = strings.TrimSpace(reason)
	if utf8.RuneCountInString(reason) > entryRotationReasonMax {
		reason = string([]rune(reason)[:entryRotationReasonMax])
	}
	res, err := db.DB.ExecContext(ctx, entryRotationQuery, id, to, entryDetail(reason), by, pq.Array(from))
	if err != nil {
		return fmt.Errorf("save entry %d rotation: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrRotationChanged
	}
	return nil
}
