# Merge standalone + DPM monthly series into a combined total.
#
# Bindings (via --argjson):
#   $standalone  [{month,downloads}, ...]
#   $dpm         [{month,downloads}, ...]
# Output: [{ month, downloads, standalone, dpm }, ...] oldest first.
# Gap-fills every calendar month between the first and last observed month.

def month_index:
  ((.[0:4] | tonumber) * 12) + (.[5:7] | tonumber);

def from_month_index:
  ((. - 1) / 12 | floor) as $y
  | ((. - 1) % 12 + 1) as $m
  | ($m | tostring | if length == 1 then "0\(.)" else . end) as $mm
  | "\($y)-\($mm)";

($standalone // []) as $s
| ($dpm // []) as $d
| (($s + $d) | map(.month) | unique | sort) as $months
| if ($months | length) == 0 then []
  else
    ($months | map(month_index) | min) as $first
    | ($months | map(month_index) | max) as $last
    | ($s | map({(.month): .downloads}) | add // {}) as $by_s
    | ($d | map({(.month): .downloads}) | add // {}) as $by_d
    | [ range($first; $last + 1)
        | from_month_index as $m
        | ($by_s[$m] // 0) as $sv
        | ($by_d[$m] // 0) as $dv
        | { month: $m, standalone: $sv, dpm: $dv, downloads: ($sv + $dv) } ]
  end
