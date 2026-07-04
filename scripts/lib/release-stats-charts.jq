# Renders one hand-rolled SVG chart from the normalized release-stats
# dataset (see release-stats-process.jq). No external chart library —
# pure jq computing coordinates, so output is deterministic and
# diffable across runs.
#
# The underlying metric (sum of GitHub release-asset download_count per
# release) mirrors the client-side logic in
# https://github.com/RamiAwar/gh-release-stats, reimplemented here as a
# static, committed chart since GitHub README markdown cannot execute
# the JS that tool relies on.
#
# Invoked as:
#   jq -nr --argjson data "$DATA" --arg mode <mode> -f release-stats-charts.jq
# where mode is one of: by_platform | by_version | totals_by_version | totals_by_platform

def r2: (. * 100 | round) / 100;

def display_name:
  {
    "darwin_arm64": "macOS (arm64)",
    "linux_amd64":  "Linux (amd64)",
    "windows_amd64":"Windows (amd64)",
    "deb":          "Debian (.deb)"
  }[.];

def color_of:
  {
    "darwin_arm64": "#2563eb",
    "linux_amd64":  "#16a34a",
    "windows_amd64":"#dc2626",
    "deb":          "#9333ea"
  }[.];

def svg_header($w; $h; $title):
  "<svg xmlns=\"http://www.w3.org/2000/svg\" width=\"\($w)\" height=\"\($h)\" viewBox=\"0 0 \($w) \($h)\" font-family=\"-apple-system,Segoe UI,Helvetica,Arial,sans-serif\" role=\"img\" aria-label=\"\($title)\">\n" +
  "<rect x=\"0\" y=\"0\" width=\"\($w)\" height=\"\($h)\" fill=\"#ffffff\"/>\n" +
  "<text x=\"\($w/2)\" y=\"20\" text-anchor=\"middle\" font-size=\"14\" font-weight=\"600\" fill=\"#111827\">\($title)</text>\n";

def svg_footer: "</svg>\n";

# ---- Line chart: one or more series over the release timeline ----
def line_chart($title; $series; $labels; $fillFirst):
  (820) as $w
  | (440) as $h
  | (55) as $mLeft
  | (if ($series|length) > 1 then 190 else 40 end) as $mRight
  | (30) as $mTop
  | (80) as $mBottom
  | ($w - $mLeft - $mRight) as $plotW
  | ($h - $mTop - $mBottom) as $plotH
  | ($labels|length) as $n
  | (if $n > 1 then $plotW / ($n - 1) else 0 end) as $xstep
  | ([$series[].values[]] | max // 0) as $rawMax
  | (if $rawMax <= 0 then 1 else $rawMax end) as $maxY
  | ($maxY * 1.15) as $topY
  | (def xpos($i): $mLeft + ($i * $xstep) | r2;
     def ypos($v): $mTop + $plotH - (($v / $topY) * $plotH) | r2;

     (svg_header($w; $h; $title)) as $head

     # gridlines + y-axis labels (0/25/50/75/100%)
     | ([range(0;5)] | map(
         . as $g
         | ($topY * $g / 4) as $val
         | (ypos($val)) as $gy
         | "<line x1=\"\($mLeft)\" y1=\"\($gy)\" x2=\"\($mLeft + $plotW)\" y2=\"\($gy)\" stroke=\"#e5e7eb\" stroke-width=\"1\"/>\n" +
           "<text x=\"\($mLeft - 8)\" y=\"\($gy + 4)\" text-anchor=\"end\" font-size=\"10\" fill=\"#6b7280\">\($val|round)</text>\n"
       ) | join("")) as $grid

     # x-axis labels (release tags), rotated for readability
     | ($labels | to_entries | map(
         .key as $i | .value as $lab
         | (xpos($i)) as $lx
         | "<text x=\"\($lx)\" y=\"\($mTop + $plotH + 16)\" text-anchor=\"end\" font-size=\"10\" fill=\"#374151\" transform=\"rotate(-40 \($lx) \($mTop + $plotH + 16))\">\($lab)</text>\n"
       ) | join("")) as $xlabels

     # axes
     | ("<line x1=\"\($mLeft)\" y1=\"\($mTop)\" x2=\"\($mLeft)\" y2=\"\($mTop + $plotH)\" stroke=\"#9ca3af\" stroke-width=\"1\"/>\n" +
        "<line x1=\"\($mLeft)\" y1=\"\($mTop + $plotH)\" x2=\"\($mLeft + $plotW)\" y2=\"\($mTop + $plotH)\" stroke=\"#9ca3af\" stroke-width=\"1\"/>\n") as $axes

     # series: optional area fill for the first/only series, lines + point markers for all
     | ($series | to_entries | map(
         .key as $si | .value as $s
         | ($s.values | to_entries | map("\(xpos(.key)),\(ypos(.value))") | join(" ")) as $pts
         | (if $fillFirst and $si == 0 then
              "<polygon points=\"\($mLeft),\($mTop + $plotH) \($pts) \($mLeft + $plotW),\($mTop + $plotH)\" fill=\"\($s.color)\" fill-opacity=\"0.12\"/>\n"
            else "" end) +
           "<polyline points=\"\($pts)\" fill=\"none\" stroke=\"\($s.color)\" stroke-width=\"2.5\"/>\n" +
           ($s.values | to_entries | map(
              "<circle cx=\"\(xpos(.key))\" cy=\"\(ypos(.value))\" r=\"3\" fill=\"\($s.color)\"/>\n"
           ) | join(""))
       ) | join("")) as $lines

     # legend (only meaningful with multiple series)
     | (if ($series|length) > 1 then
          ($series | to_entries | map(
            .key as $i | .value as $s
            | ($mTop + 10 + $i * 20) as $ly
            | "<rect x=\"\($mLeft + $plotW + 20)\" y=\"\($ly - 9)\" width=\"10\" height=\"10\" fill=\"\($s.color)\"/>\n" +
              "<text x=\"\($mLeft + $plotW + 34)\" y=\"\($ly)\" font-size=\"11\" fill=\"#111827\">\($s.label)</text>\n"
          ) | join(""))
        else "" end) as $legend

     | "<text x=\"14\" y=\"\($mTop + $plotH/2)\" text-anchor=\"middle\" font-size=\"11\" fill=\"#6b7280\" transform=\"rotate(-90 14 \($mTop + $plotH/2))\">Downloads</text>\n" as $ylabel

     | $head + $grid + $axes + $lines + $xlabels + $legend + $ylabel + svg_footer
    );

# ---- Bar chart: one value per category ----
def bar_chart($title; $bars):
  # $bars = [{label, value, color}]
  (760) as $w
  | (420) as $h
  | (55) as $mLeft
  | (30) as $mRight
  | (30) as $mTop
  | (100) as $mBottom
  | ($w - $mLeft - $mRight) as $plotW
  | ($h - $mTop - $mBottom) as $plotH
  | ($bars|length) as $n
  | (if $n > 0 then $plotW / $n else $plotW end) as $slot
  | ($slot * 0.55) as $barW
  | ([$bars[].value] | max // 0) as $rawMax
  | (if $rawMax <= 0 then 1 else $rawMax end) as $maxY
  | ($maxY * 1.15) as $topY
  | (def ytop($v): $mTop + $plotH - (($v / $topY) * $plotH) | r2;

     (svg_header($w; $h; $title)) as $head

     | ([range(0;5)] | map(
         . as $g
         | ($topY * $g / 4) as $val
         | (ytop($val)) as $gy
         | "<line x1=\"\($mLeft)\" y1=\"\($gy)\" x2=\"\($mLeft + $plotW)\" y2=\"\($gy)\" stroke=\"#e5e7eb\" stroke-width=\"1\"/>\n" +
           "<text x=\"\($mLeft - 8)\" y=\"\($gy + 4)\" text-anchor=\"end\" font-size=\"10\" fill=\"#6b7280\">\($val|round)</text>\n"
       ) | join("")) as $grid

     | ("<line x1=\"\($mLeft)\" y1=\"\($mTop)\" x2=\"\($mLeft)\" y2=\"\($mTop + $plotH)\" stroke=\"#9ca3af\" stroke-width=\"1\"/>\n" +
        "<line x1=\"\($mLeft)\" y1=\"\($mTop + $plotH)\" x2=\"\($mLeft + $plotW)\" y2=\"\($mTop + $plotH)\" stroke=\"#9ca3af\" stroke-width=\"1\"/>\n") as $axes

     | ($bars | to_entries | map(
         .key as $i | .value as $b
         | ($mLeft + $i * $slot + ($slot - $barW)/2) as $bx
         | (ytop($b.value)) as $by
         | ($mTop + $plotH - $by) as $bh
         | ($bx + $barW/2) as $cx
         | "<rect x=\"\($bx|r2)\" y=\"\($by)\" width=\"\($barW|r2)\" height=\"\($bh|r2)\" fill=\"\($b.color)\"/>\n" +
           "<text x=\"\($cx|r2)\" y=\"\($by - 5)\" text-anchor=\"middle\" font-size=\"10\" fill=\"#111827\">\($b.value)</text>\n" +
           "<text x=\"\($cx|r2)\" y=\"\($mTop + $plotH + 16)\" text-anchor=\"end\" font-size=\"10\" fill=\"#374151\" transform=\"rotate(-40 \($cx|r2) \($mTop + $plotH + 16))\">\($b.label)</text>\n"
       ) | join("")) as $barsSvg

     | "<text x=\"14\" y=\"\($mTop + $plotH/2)\" text-anchor=\"middle\" font-size=\"11\" fill=\"#6b7280\" transform=\"rotate(-90 14 \($mTop + $plotH/2))\">Downloads</text>\n" as $ylabel

     | $head + $grid + $axes + $barsSvg + $ylabel + svg_footer
    );

($data) as $d
# Debian (.deb) downloads are excluded from the charts below — it's a
# Linux packaging format rather than a distinct platform, and its
# near-zero counts add legend/bar noise. The underlying data model
# ($d.platforms, $d.releases[].by_platform, $d.platform_totals) still
# carries "deb", so the markdown table and any other consumer of this
# snapshot keep the full breakdown; only chart rendering filters it out.
| ($d.platforms | map(select(. != "deb"))) as $chartPlatforms
| ($d.releases | map(.tag)) as $labels
| (
  if $mode == "by_platform" then
    ($chartPlatforms | map({label: display_name, color: color_of}) ) as $meta
    | ($chartPlatforms | to_entries | map(
        .key as $i | .value as $p
        | {label: ($meta[$i].label), color: ($meta[$i].color), values: ($d.releases | map(.by_platform[$p]))}
      )) as $series
    | line_chart("Downloads per platform, per release — homebrew-canton-devkit"; $series; $labels; false)

  elif $mode == "by_version" then
    ([{label: "Total downloads", color: "#0d9488", values: ($d.releases | map(.total))}]) as $series
    | line_chart("Total downloads per release — homebrew-canton-devkit"; $series; $labels; true)

  elif $mode == "totals_by_version" then
    ($d.releases | map({label: .tag, value: .total, color: "#0d9488"})) as $bars
    | bar_chart("All-time downloads per release — homebrew-canton-devkit"; $bars)

  elif $mode == "totals_by_platform" then
    ($chartPlatforms | map({label: display_name, value: $d.platform_totals[.], color: color_of})) as $bars
    | bar_chart("All-time downloads per platform — homebrew-canton-devkit"; $bars)

  else
    error("unknown mode: \($mode)")
  end
)
