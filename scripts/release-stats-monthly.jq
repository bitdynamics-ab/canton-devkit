# Monthly download counts from cumulative daily snapshots.
#
# Input: JSON array of { date, total, ... } (use jq -s on a JSONL history file).
# Output: [ { month: "YYYY-MM", downloads: N }, ... ] oldest first.
# Missing calendar months between the first and last observed delta month are filled with 0.
# The first snapshot is baseline only and contributes no delta. Negative steps clamp to 0.

def year_month:
  .[0:7];

def month_index:
  ((.[0:4] | tonumber) * 12) + (.[5:7] | tonumber);

def from_month_index:
  ((. - 1) / 12 | floor) as $y
  | ((. - 1) % 12 + 1) as $m
  | ($m | tostring | if length == 1 then "0\(.)" else . end) as $mm
  | "\($y)-\($mm)";

sort_by(.date)
| . as $rows
| [ range(1; length)
    | { month: ($rows[.].date | year_month),
        idx: ($rows[.].date | year_month | month_index),
        delta: ([($rows[.].total - $rows[.-1].total), 0] | max) } ]
| group_by(.month)
| map({ month: .[0].month, idx: .[0].idx, downloads: (map(.delta) | add) })
| . as $observed
| if ($observed | length) == 0 then []
  else
    ($observed | map(.idx) | min) as $first
    | ($observed | map(.idx) | max) as $last
    | ($observed | map({(.month): .downloads}) | add) as $by_month
    | [ range($first; $last + 1)
        | from_month_index as $m
        | { month: $m, downloads: ($by_month[$m] // 0) } ]
  end
