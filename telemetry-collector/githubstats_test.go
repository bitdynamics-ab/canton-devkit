package collector

import "testing"

// Recorded GitHub API fixtures (trimmed to the fields we read).
const releasesFixture = `[
  {"tag_name":"v0.3.0","assets":[
    {"name":"canton-devkit_linux_amd64","download_count":120},
    {"name":"canton-devkit_darwin_arm64","download_count":85},
    {"name":"canton-devkit_windows_amd64.exe","download_count":17}
  ]},
  {"tag_name":"v0.2.0","assets":[
    {"name":"canton-devkit_linux_amd64","download_count":40}
  ]},
  {"tag_name":"v0.1.0-notes-only","assets":[]}
]`

const repoFixture = `{
  "stargazers_count": 42,
  "forks_count": 7,
  "subscribers_count": 9,
  "open_issues_count": 3,
  "full_name": "bitdynamics-ab/canton-devkit"
}`

func TestParseReleases(t *testing.T) {
	dls, err := parseReleases([]byte(releasesFixture))
	if err != nil {
		t.Fatal(err)
	}
	if len(dls) != 4 { // 3 assets on v0.3.0 + 1 on v0.2.0; the notes-only release has none
		t.Fatalf("got %d asset rows, want 4: %+v", len(dls), dls)
	}
	total := 0
	for _, d := range dls {
		total += d.DownloadCount
	}
	if total != 262 { // 120+85+17+40
		t.Errorf("cumulative downloads = %d, want 262", total)
	}
	// Spot-check a row carries tag + asset + count.
	found := false
	for _, d := range dls {
		if d.Tag == "v0.3.0" && d.Asset == "canton-devkit_windows_amd64.exe" {
			found = true
			if d.DownloadCount != 17 {
				t.Errorf("windows asset count = %d, want 17", d.DownloadCount)
			}
		}
	}
	if !found {
		t.Error("expected the v0.3.0 windows asset row")
	}
}

func TestParseRepo(t *testing.T) {
	st, err := parseRepo([]byte(repoFixture))
	if err != nil {
		t.Fatal(err)
	}
	if st.Stars != 42 || st.Forks != 7 || st.Watchers != 9 || st.OpenIssues != 3 {
		t.Errorf("parsed stats wrong: %+v", st)
	}
}

func TestParseReleases_Empty(t *testing.T) {
	dls, err := parseReleases([]byte(`[]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(dls) != 0 {
		t.Errorf("empty releases should yield 0 rows, got %d", len(dls))
	}
}
