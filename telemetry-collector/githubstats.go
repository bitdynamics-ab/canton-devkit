package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// GitHub adoption signals — release-asset download counts and repo
// visibility (stars/forks/watchers). Zero-PII telemetry can't count
// unique installs, so a scheduled run snapshots these daily into
// Postgres for Metabase to chart download and visibility trends.

// AssetDownload is one release asset's cumulative download count, as
// reported by the GitHub API (download_count is cumulative since
// publish).
type AssetDownload struct {
	Tag           string
	Asset         string
	DownloadCount int
}

// RepoStats is the repo's visibility snapshot.
type RepoStats struct {
	Stars      int
	Forks      int
	Watchers   int
	OpenIssues int
}

// ghRelease / ghRepo mirror just the GitHub API fields we read.
type ghRelease struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name          string `json:"name"`
		DownloadCount int    `json:"download_count"`
	} `json:"assets"`
}

type ghRepo struct {
	StargazersCount int `json:"stargazers_count"`
	ForksCount      int `json:"forks_count"`
	SubscribersHint int `json:"subscribers_count"` // "watchers" in the UI sense
	OpenIssuesCount int `json:"open_issues_count"`
}

// parseReleases extracts per-asset download counts from a GitHub
// /releases response body. Pure (no I/O) so it's unit-testable against
// recorded fixtures.
func parseReleases(body []byte) ([]AssetDownload, error) {
	var rels []ghRelease
	if err := json.Unmarshal(body, &rels); err != nil {
		return nil, fmt.Errorf("decode releases: %w", err)
	}
	var out []AssetDownload
	for _, r := range rels {
		for _, a := range r.Assets {
			out = append(out, AssetDownload{Tag: r.TagName, Asset: a.Name, DownloadCount: a.DownloadCount})
		}
	}
	return out, nil
}

// parseRepo extracts the visibility snapshot from a GitHub /repos/{repo}
// response body.
func parseRepo(body []byte) (RepoStats, error) {
	var r ghRepo
	if err := json.Unmarshal(body, &r); err != nil {
		return RepoStats{}, fmt.Errorf("decode repo: %w", err)
	}
	return RepoStats{
		Stars:      r.StargazersCount,
		Forks:      r.ForksCount,
		Watchers:   r.SubscribersHint,
		OpenIssues: r.OpenIssuesCount,
	}, nil
}

// GitHubClient fetches adoption signals from the GitHub REST API.
type GitHubClient struct {
	repo  string // "owner/name"
	token string // optional; required for private repos and higher rate limits
	http  *http.Client
	base  string // API base, overridable in tests
}

// NewGitHubClient builds a client for "owner/name". token may be "".
func NewGitHubClient(repo, token string) *GitHubClient {
	return &GitHubClient{
		repo:  repo,
		token: token,
		http:  &http.Client{Timeout: 15 * time.Second},
		base:  "https://api.github.com",
	}
}

func (c *GitHubClient) get(ctx context.Context, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("GET %s: HTTP %d", path, resp.StatusCode)
	}
	return body, nil
}

// Downloads fetches per-asset cumulative download counts across releases.
func (c *GitHubClient) Downloads(ctx context.Context) ([]AssetDownload, error) {
	body, err := c.get(ctx, "/repos/"+c.repo+"/releases?per_page=100")
	if err != nil {
		return nil, err
	}
	return parseReleases(body)
}

// Stats fetches the repo visibility snapshot.
func (c *GitHubClient) Stats(ctx context.Context) (RepoStats, error) {
	body, err := c.get(ctx, "/repos/"+c.repo)
	if err != nil {
		return RepoStats{}, err
	}
	return parseRepo(body)
}

// GitHubStore persists GitHub snapshots into Postgres.
type GitHubStore struct{ pool *pgxpool.Pool }

// NewGitHubStore opens a pool against dsn and verifies it.
func NewGitHubStore(ctx context.Context, dsn string) (*GitHubStore, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("open pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	return &GitHubStore{pool: pool}, nil
}

// Close releases the pool.
func (s *GitHubStore) Close() { s.pool.Close() }

// SaveDownloads upserts a day's per-asset download snapshot. capturedOn
// keys the row so re-running on the same day replaces (idempotent).
func (s *GitHubStore) SaveDownloads(ctx context.Context, capturedOn time.Time, dls []AssetDownload) error {
	if len(dls) == 0 {
		return nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	batch := &pgx.Batch{}
	const q = `
INSERT INTO github_release_downloads (captured_on, tag, asset, download_count)
VALUES ($1, $2, $3, $4)
ON CONFLICT (captured_on, tag, asset)
DO UPDATE SET download_count = EXCLUDED.download_count`
	for _, d := range dls {
		batch.Queue(q, capturedOn, d.Tag, d.Asset, d.DownloadCount)
	}
	br := tx.SendBatch(ctx, batch)
	for range dls {
		if _, err := br.Exec(); err != nil {
			_ = br.Close()
			return fmt.Errorf("exec download upsert: %w", err)
		}
	}
	if err := br.Close(); err != nil {
		return fmt.Errorf("close batch: %w", err)
	}
	return tx.Commit(ctx)
}

// SaveStats upserts a day's repo visibility snapshot.
func (s *GitHubStore) SaveStats(ctx context.Context, capturedOn time.Time, st RepoStats) error {
	const q = `
INSERT INTO github_repo_stats (captured_on, stars, forks, watchers, open_issues)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (captured_on)
DO UPDATE SET stars = EXCLUDED.stars, forks = EXCLUDED.forks,
              watchers = EXCLUDED.watchers, open_issues = EXCLUDED.open_issues`
	_, err := s.pool.Exec(ctx, q, capturedOn, st.Stars, st.Forks, st.Watchers, st.OpenIssues)
	if err != nil {
		return fmt.Errorf("exec stats upsert: %w", err)
	}
	return nil
}
