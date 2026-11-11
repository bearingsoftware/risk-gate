package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const perPage = 100

type client struct {
	owner string
	cache string
	calls atomic.Int64
}

type prRec struct {
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
	Head struct {
		Ref string `json:"ref"`
	} `json:"head"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	MergedAt  *time.Time `json:"merged_at"`
}

type prDetail struct {
	ChangedFiles int `json:"changed_files"`
}

type fileRec struct {
	Filename         string `json:"filename"`
	PreviousFilename string `json:"previous_filename"`
	Status           string `json:"status"`
	Additions        int    `json:"additions"`
	Deletions        int    `json:"deletions"`
}

type commitRec struct {
	Author *struct {
		Login string `json:"login"`
	} `json:"author"`
	Commit struct {
		Committer struct {
			Date time.Time `json:"date"`
		} `json:"committer"`
	} `json:"commit"`
}

// gh runs one `gh api` call and counts it against the run's budget.
func (c *client) gh(path string, extra ...string) ([]byte, error) {
	c.calls.Add(1)
	cmd := exec.Command("gh", append([]string{"api", path}, extra...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("gh api %s: %w: %s", path, err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

func (c *client) apiPath(repo, rel string) string {
	return fmt.Sprintf("repos/%s/%s/%s", c.owner, repo, rel)
}

func (c *client) repoFile(repo, rel string) string {
	return filepath.Join(c.cache, repo, rel)
}

func (c *client) mergedList(repo, branch string, cutoff time.Time) ([]prRec, error) {
	q := url.Values{"state": {"closed"}, "base": {branch}, "sort": {"updated"}, "direction": {"desc"}}
	return cachedJSON(c.repoFile(repo, "pulls-"+safe(branch)+".json"), func() ([]prRec, error) {
		return fetchPages[prRec](c, c.apiPath(repo, "pulls"), q, func(b []prRec) bool {
			return b[len(b)-1].UpdatedAt.Before(cutoff)
		})
	})
}

func (c *client) files(repo string, n int) ([]fileRec, error) {
	return cachedJSON(c.repoFile(repo, fmt.Sprintf("files/%d.json", n)), func() ([]fileRec, error) {
		return fetchPages[fileRec](c, c.apiPath(repo, fmt.Sprintf("pulls/%d/files", n)), url.Values{}, nil)
	})
}

func (c *client) detail(repo string, n int) (prDetail, error) {
	return cachedJSON(c.repoFile(repo, fmt.Sprintf("pr/%d.json", n)), func() (prDetail, error) {
		var d prDetail
		out, err := c.gh(c.apiPath(repo, fmt.Sprintf("pulls/%d", n)))
		if err != nil {
			return d, err
		}
		return d, json.Unmarshal(out, &d)
	})
}

func (c *client) commits(repo, branch string, since time.Time) ([]commitRec, error) {
	q := url.Values{"sha": {branch}, "since": {since.UTC().Format(time.RFC3339)}}
	file := fmt.Sprintf("commits-%s-since-%s.json", safe(branch), since.Format("2006-01-02"))
	return cachedJSON(c.repoFile(repo, file), func() ([]commitRec, error) {
		return fetchPages[commitRec](c, c.apiPath(repo, "commits"), q, nil)
	})
}

func (c *client) tiersFile(repo, ref string) ([]byte, error) {
	return cachedBytes(c.repoFile(repo, "review-tiers-"+safe(ref)+".yml"), func() ([]byte, error) {
		return c.gh(c.apiPath(repo, "contents/.github/review-tiers.yml")+"?ref="+url.QueryEscape(ref),
			"-H", "Accept: application/vnd.github.raw")
	})
}

// fetchPages walks a paginated list until a short page, or until done reports the listing is past the window.
func fetchPages[T any](c *client, path string, q url.Values, done func([]T) bool) ([]T, error) {
	var all []T
	for page := 1; ; page++ {
		qq := url.Values{}
		for k, v := range q {
			qq[k] = v
		}
		qq.Set("per_page", strconv.Itoa(perPage))
		qq.Set("page", strconv.Itoa(page))
		out, err := c.gh(path + "?" + qq.Encode())
		if err != nil {
			return nil, err
		}
		var batch []T
		if err := json.Unmarshal(out, &batch); err != nil {
			return nil, fmt.Errorf("decoding %s page %d: %w", path, page, err)
		}
		all = append(all, batch...)
		if len(batch) < perPage || (done != nil && done(batch)) {
			return all, nil
		}
	}
}

func cachedJSON[T any](file string, fill func() (T, error)) (T, error) {
	var zero T
	b, err := os.ReadFile(file)
	if err == nil {
		var v T
		if err := json.Unmarshal(b, &v); err != nil {
			return zero, fmt.Errorf("decoding cache %s: %w", file, err)
		}
		return v, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return zero, fmt.Errorf("reading cache %s: %w", file, err)
	}
	v, err := fill()
	if err != nil {
		return zero, err
	}
	b, err = json.Marshal(v)
	if err != nil {
		return zero, fmt.Errorf("encoding cache %s: %w", file, err)
	}
	if err := writeAtomic(file, b); err != nil {
		return zero, err
	}
	return v, nil
}

func cachedBytes(file string, fill func() ([]byte, error)) ([]byte, error) {
	b, err := os.ReadFile(file)
	if err == nil {
		return b, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("reading cache %s: %w", file, err)
	}
	b, err = fill()
	if err != nil {
		return nil, err
	}
	return b, writeAtomic(file, b)
}

func writeAtomic(file string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return fmt.Errorf("creating cache dir: %w", err)
	}
	tmp := file + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return fmt.Errorf("writing cache %s: %w", file, err)
	}
	if err := os.Rename(tmp, file); err != nil {
		return fmt.Errorf("writing cache %s: %w", file, err)
	}
	return nil
}

// forEach runs fn over items with a fixed worker pool and returns the first error.
func forEach[T any](items []T, workers int, fn func(T) error) error {
	ch := make(chan T)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var first error
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for it := range ch {
				if err := fn(it); err != nil {
					mu.Lock()
					if first == nil {
						first = err
					}
					mu.Unlock()
				}
			}
		}()
	}
	for _, it := range items {
		ch <- it
	}
	close(ch)
	wg.Wait()
	return first
}

func safe(s string) string { return strings.ReplaceAll(s, "/", "_") }
