# Daily download counts from cumulative daily snapshots.
#
# Input: JSON array of { date, total, ... } (use jq -s on a JSONL history file).
# Output: [ { date: "YYYY-MM-DD", downloads: N }, ... ] oldest first.
# The first snapshot is baseline only and contributes no delta. Negative steps clamp to 0.

sort_by(.date)
| . as $rows
| [ range(1; length)
    | { date: $rows[.].date,
        downloads: ([($rows[.].total - $rows[.-1].total), 0] | max) } ]
