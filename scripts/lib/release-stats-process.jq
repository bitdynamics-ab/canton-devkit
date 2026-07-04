# Normalizes the raw GitHub Releases API payload for
# bitdynamics-ab/homebrew-canton-devkit into a compact shape the chart
# renderer (release-stats-charts.jq) and the markdown table renderer
# (release-stats-table.jq) both consume.
#
# Input: a flat JSON array of release objects, as returned by
#   gh api repos/<owner>/<repo>/releases --paginate
# (this script slurps every page into one array before running this
# filter — see scripts/release-stats.sh).
#
# Assets are classified into a fixed platform set by filename pattern,
# not by exact name, because asset naming has drifted across releases
# (e.g. v0.4 shipped "canton-devkit_windows_amd64.exe" while v0.9.0+
# ships "canton-devkit_v0.9.0_windows_amd64.zip"). Checksum manifests
# (SHA256SUMS / checksums.txt) intentionally match no platform and are
# excluded from download totals.

def platform_of($name):
  if   ($name | test("_darwin_arm64\\."))            then "darwin_arm64"
  elif ($name | test("_linux_amd64\\."))              then "linux_amd64"
  elif ($name | test("_windows_amd64\\.(zip|exe)$"))  then "windows_amd64"
  elif ($name | test("_amd64\\.deb$"))                then "deb"
  else null
  end;

def platform_order: ["darwin_arm64", "linux_amd64", "windows_amd64", "deb"];

(add // []) as $all
| ($all | sort_by(.published_at)) as $sorted
| ($sorted | map(
    {
      tag: .tag_name,
      date: ((.published_at // .created_at) // "")[0:10],
      by_platform: (reduce (.assets[]) as $a
        ({}; ($a.name | platform_of(.)) as $p
          | if $p then .[$p] = ((.[$p] // 0) + $a.download_count) else . end))
    }
  )) as $withRaw
| ($withRaw | map(
    . as $r
    | $r + {
        by_platform: (platform_order | map({(.): ($r.by_platform[.] // 0)}) | add),
        total: ([$r.by_platform[]] | add // 0)
      }
  )) as $releases
| {
    platforms: platform_order,
    releases: $releases,
    platform_totals: (platform_order
      | map({(.): (reduce ($releases[].by_platform[.] // 0) as $v (0; . + $v))})
      | add)
  }
