// Package gh is the GitHub REST client behind the interface the run package consumes.
package gh

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	DefaultBaseURL = "https://api.github.com"
	perPage        = 100
	maxFilePages   = 30
	maxCommitPages = 20
)

type PullRequest struct {
	Number       int
	Title        string
	Body         string
	Labels       []string
	Author       string
	AuthorBot    bool
	BaseRef      string
	BaseSHA      string
	HeadRef      string
	HeadSHA      string
	ChangedFiles int
}

type File struct {
	Path         string
	PreviousPath string
	Status       string
	Additions    int
	Deletions    int
}

type LabelEvent struct {
	Actor string
	At    time.Time
}

type MergedPRs struct {
	Files    [][]string
	Examined int
	Capped   bool
}

type Client struct {
	BaseURL string
	Repo    string
	Token   string
	HTTP    *http.Client
	// Sleep waits between retries; nil means a real timer.
	Sleep func(context.Context, time.Duration) error

	mu     sync.Mutex
	budget time.Duration
}

// RetryBudget is the total backoff wait one client may spend across all calls.
const RetryBudget = 3 * time.Minute

func (c *Client) spend(d time.Duration) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if d > c.budget {
		return false
	}
	c.budget -= d
	return true
}

func New(repo, token string) *Client {
	return &Client{BaseURL: DefaultBaseURL, Repo: repo, Token: token, HTTP: &http.Client{Timeout: 30 * time.Second}, budget: RetryBudget}
}

type StatusError struct {
	Method string
	Path   string
	Status int
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("github %s %s: status %d", e.Method, e.Path, e.Status)
}

const maxAttempts = 3

func (c *Client) sleep(ctx context.Context, d time.Duration) error {
	if c.Sleep != nil {
		return c.Sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// retryDelay says whether a response is worth retrying and how long to wait first.
func retryDelay(resp *http.Response, attempt int) (time.Duration, bool) {
	backoff := time.Duration(1<<attempt) * time.Second
	ra := resp.Header.Get("Retry-After")
	switch {
	case resp.StatusCode == http.StatusTooManyRequests, resp.StatusCode >= 500:
	case resp.StatusCode == http.StatusForbidden && (ra != "" || resp.Header.Get("X-RateLimit-Remaining") == "0"):
	default:
		return 0, false
	}
	if secs, err := strconv.Atoi(ra); err == nil && secs >= 0 {
		return min(time.Duration(secs)*time.Second, maxRetryWait), true
	}
	if reset, err := strconv.ParseInt(resp.Header.Get("X-RateLimit-Reset"), 10, 64); err == nil && resp.Header.Get("X-RateLimit-Remaining") == "0" {
		if d := time.Until(time.Unix(reset, 0)); d > 0 {
			return min(d, maxRetryWait), true
		}
	}
	return backoff, true
}

const maxRetryWait = 60 * time.Second

func (c *Client) do(ctx context.Context, method, p string, body, out any) error {
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return fmt.Errorf("encoding request: %w", err)
		}
	}
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		var rdr io.Reader
		if payload != nil {
			rdr = bytes.NewReader(payload)
		}
		req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+p, rdr)
		if err != nil {
			return fmt.Errorf("building request: %w", err)
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		if c.Token != "" {
			req.Header.Set("Authorization", "Bearer "+c.Token)
		}
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := c.HTTP.Do(req)
		var wait time.Duration
		switch {
		case err != nil:
			if ctx.Err() != nil {
				return fmt.Errorf("github %s %s: %w", method, p, err)
			}
			lastErr = fmt.Errorf("github %s %s: %w", method, p, err)
			wait = time.Duration(1<<attempt) * time.Second
		case resp.StatusCode >= 200 && resp.StatusCode <= 299:
			defer resp.Body.Close()
			if out == nil {
				return nil
			}
			if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
				return fmt.Errorf("decoding github %s %s: %w", method, p, err)
			}
			return nil
		default:
			resp.Body.Close()
			lastErr = &StatusError{Method: method, Path: p, Status: resp.StatusCode}
			d, retry := retryDelay(resp, attempt)
			if !retry {
				return lastErr
			}
			wait = d
		}
		if attempt == maxAttempts-1 || !c.spend(wait) {
			break
		}
		if err := c.sleep(ctx, wait); err != nil {
			return fmt.Errorf("github %s %s: %w", method, p, err)
		}
	}
	return lastErr
}

func (c *Client) PullRequest(ctx context.Context, n int) (PullRequest, error) {
	var raw struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
		Body   string `json:"body"`
		Labels []struct {
			Name string `json:"name"`
		} `json:"labels"`
		User struct {
			Login string `json:"login"`
			Type  string `json:"type"`
		} `json:"user"`
		Base         struct{ Ref, SHA string } `json:"base"`
		Head         struct{ Ref, SHA string } `json:"head"`
		ChangedFiles int                       `json:"changed_files"`
	}
	if err := c.do(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/pulls/%d", c.Repo, n), nil, &raw); err != nil {
		return PullRequest{}, err
	}
	pr := PullRequest{
		Number: raw.Number, Title: raw.Title, Body: raw.Body,
		Author: raw.User.Login, AuthorBot: raw.User.Type == "Bot" || strings.HasSuffix(raw.User.Login, "[bot]"),
		BaseRef: raw.Base.Ref, BaseSHA: raw.Base.SHA, HeadRef: raw.Head.Ref, HeadSHA: raw.Head.SHA,
		ChangedFiles: raw.ChangedFiles, Labels: []string{},
	}
	for _, l := range raw.Labels {
		pr.Labels = append(pr.Labels, l.Name)
	}
	return pr, nil
}

type rawFile struct {
	Filename         string `json:"filename"`
	PreviousFilename string `json:"previous_filename"`
	Status           string `json:"status"`
	Additions        int    `json:"additions"`
	Deletions        int    `json:"deletions"`
}

func (c *Client) prFiles(ctx context.Context, n int) ([]rawFile, error) {
	var all []rawFile
	for page := 1; page <= maxFilePages; page++ {
		var batch []rawFile
		p := fmt.Sprintf("/repos/%s/pulls/%d/files?per_page=%d&page=%d", c.Repo, n, perPage, page)
		if err := c.do(ctx, http.MethodGet, p, nil, &batch); err != nil {
			return nil, err
		}
		all = append(all, batch...)
		if len(batch) < perPage {
			break
		}
	}
	return all, nil
}

// Files lists a PR's changed files; GitHub returns at most 3000.
func (c *Client) Files(ctx context.Context, n int) ([]File, error) {
	raw, err := c.prFiles(ctx, n)
	if err != nil {
		return nil, err
	}
	out := make([]File, len(raw))
	for i, f := range raw {
		out[i] = File{Path: f.Filename, PreviousPath: f.PreviousFilename, Status: f.Status, Additions: f.Additions, Deletions: f.Deletions}
	}
	return out, nil
}

// AuthorCommits counts commits by login on branch since the given time (all time when zero),
// stopping once max is reached.
func (c *Client) AuthorCommits(ctx context.Context, login, branch string, since time.Time, max int) (int, error) {
	n := 0
	for page := 1; page <= maxCommitPages; page++ {
		q := url.Values{"sha": {branch}, "author": {login}, "per_page": {fmt.Sprint(perPage)}, "page": {fmt.Sprint(page)}}
		if !since.IsZero() {
			q.Set("since", since.UTC().Format(time.RFC3339))
		}
		var batch []struct {
			SHA string `json:"sha"`
		}
		if err := c.do(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/commits?%s", c.Repo, q.Encode()), nil, &batch); err != nil {
			return 0, err
		}
		n += len(batch)
		if n >= max || len(batch) < perPage {
			break
		}
	}
	return min(n, max), nil
}

// RecentMergedPRs returns the file lists of PRs merged into branch since the given time,
// looking at no more than maxPRs recently updated closed PRs and skipping the PR excluded.
func (c *Client) RecentMergedPRs(ctx context.Context, branch string, since time.Time, maxPRs, exclude int) (MergedPRs, error) {
	q := url.Values{"state": {"closed"}, "base": {branch}, "sort": {"updated"}, "direction": {"desc"}, "per_page": {fmt.Sprint(min(maxPRs, perPage))}}
	var prs []struct {
		Number   int        `json:"number"`
		MergedAt *time.Time `json:"merged_at"`
	}
	if err := c.do(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/pulls?%s", c.Repo, q.Encode()), nil, &prs); err != nil {
		return MergedPRs{}, err
	}
	out := MergedPRs{Capped: len(prs) >= maxPRs}
	for _, p := range prs {
		if p.Number == exclude || p.MergedAt == nil || p.MergedAt.Before(since) {
			continue
		}
		files, err := c.prFiles(ctx, p.Number)
		if err != nil {
			return MergedPRs{}, err
		}
		names := make([]string, len(files))
		for i, f := range files {
			names[i] = f.Filename
		}
		out.Files = append(out.Files, names)
		out.Examined++
	}
	return out, nil
}

// LatestLabelEvent finds the most recent "labeled" event for label on the PR.
func (c *Client) LatestLabelEvent(ctx context.Context, n int, label string) (LabelEvent, bool, error) {
	var latest LabelEvent
	found := false
	for page := 1; page <= maxFilePages; page++ {
		var batch []struct {
			Event     string    `json:"event"`
			CreatedAt time.Time `json:"created_at"`
			Actor     struct {
				Login string `json:"login"`
			} `json:"actor"`
			Label struct {
				Name string `json:"name"`
			} `json:"label"`
		}
		p := fmt.Sprintf("/repos/%s/issues/%d/timeline?per_page=%d&page=%d", c.Repo, n, perPage, page)
		if err := c.do(ctx, http.MethodGet, p, nil, &batch); err != nil {
			return LabelEvent{}, false, err
		}
		for _, e := range batch {
			if e.Event == "labeled" && e.Label.Name == label && (!found || e.CreatedAt.After(latest.At)) {
				latest, found = LabelEvent{Actor: e.Actor.Login, At: e.CreatedAt}, true
			}
		}
		if len(batch) < perPage {
			break
		}
	}
	return latest, found, nil
}

// RemoveLabel deletes a label from the PR; a label that is already gone is not an error.
func (c *Client) RemoveLabel(ctx context.Context, n int, label string) error {
	p := fmt.Sprintf("/repos/%s/issues/%d/labels/%s", c.Repo, n, url.PathEscape(label))
	err := c.do(ctx, http.MethodDelete, p, nil, nil)
	var se *StatusError
	if errors.As(err, &se) && se.Status == http.StatusNotFound {
		return nil
	}
	return err
}

type CheckRun struct {
	Name       string
	HeadSHA    string
	Conclusion string
	Title      string
	Summary    string
	Text       string
}

func (c *Client) CreateCheckRun(ctx context.Context, cr CheckRun) error {
	body := map[string]any{
		"name": cr.Name, "head_sha": cr.HeadSHA, "status": "completed", "conclusion": cr.Conclusion,
		"output": map[string]string{"title": cr.Title, "summary": cr.Summary, "text": cr.Text},
	}
	return c.do(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/check-runs", c.Repo), body, nil)
}

// HeadCommitDate is the committer date of a commit.
func (c *Client) HeadCommitDate(ctx context.Context, sha string) (time.Time, error) {
	var raw struct {
		Commit struct {
			Committer struct {
				Date time.Time `json:"date"`
			} `json:"committer"`
		} `json:"commit"`
	}
	if err := c.do(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/commits/%s", c.Repo, sha), nil, &raw); err != nil {
		return time.Time{}, err
	}
	return raw.Commit.Committer.Date, nil
}

// LatestForcePush finds the most recent head_ref_force_pushed event on the PR.
func (c *Client) LatestForcePush(ctx context.Context, n int) (time.Time, bool, error) {
	var latest time.Time
	found := false
	for page := 1; page <= maxFilePages; page++ {
		var batch []struct {
			Event     string    `json:"event"`
			CreatedAt time.Time `json:"created_at"`
		}
		p := fmt.Sprintf("/repos/%s/issues/%d/timeline?per_page=%d&page=%d", c.Repo, n, perPage, page)
		if err := c.do(ctx, http.MethodGet, p, nil, &batch); err != nil {
			return time.Time{}, false, err
		}
		for _, e := range batch {
			if e.Event == "head_ref_force_pushed" && (!found || e.CreatedAt.After(latest)) {
				latest, found = e.CreatedAt, true
			}
		}
		if len(batch) < perPage {
			break
		}
	}
	return latest, found, nil
}
