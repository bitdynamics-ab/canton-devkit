# Weekly download counts from cumulative daily snapshots.
#
# Input: JSON array of { date, total, ... } (use jq -s on a JSONL history file).
# Output: [ { week: "YYYY-Www", downloads: N }, ... ] oldest first.
# Missing ISO weeks between the first and last observed delta week are filled with 0.
# The first snapshot is baseline only and contributes no delta. Negative steps clamp to 0.

def monday_ts:
  (. + "T00:00:00Z" | fromdateiso8601) as $t
  | ($t | strftime("%u") | tonumber) as $dow
  | $t - (($dow - 1) * 86400);

def iso_week:
  monday_ts | strftime("%G-W%V");

sort_by(.date)
| . as $rows
| [ range(1; length)
    | { week: ($rows[.].date | iso_week),
        monday: ($rows[.].date | monday_ts),
        delta: ([($rows[.].total - $rows[.-1].total), 0] | max) } ]
| group_by(.week)
| map({ week: .[0].week, monday: .[0].monday, downloads: (map(.delta) | add) })
| . as $observed
| if ($observed | length) == 0 then []
  else
    ($observed | map(.monday) | min) as $first
    | ($observed | map(.monday) | max) as $last
    | ($observed | map({(.week): .downloads}) | add) as $by_week
    | [ range(0; (($last - $first) / 604800 | floor) + 1)
        | ($first + . * 604800)
        | strftime("%G-W%V") as $w
        | { week: $w, downloads: ($by_week[$w] // 0) } ]
  end
