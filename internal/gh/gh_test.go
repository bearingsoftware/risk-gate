package gh

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type recorder struct {
	mu   sync.Mutex
	reqs []string
}

func (r *recorder) add(req *http.Request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reqs = append(r.reqs, req.Method+" "+req.URL.RequestURI())
}

func newClient(t *testing.T, h http.HandlerFunc) (*Client, *recorder) {
	t.Helper()
	rec := &recorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.add(r)
		if r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("auth header = %q", r.Header.Get("Authorization"))
		}
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	c := New("o/r", "tok")
	c.BaseURL = srv.URL
	return c, rec
}

func TestFilesPaginates(t *testing.T) {
	t.Parallel()
	c, rec := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		n := 100
		if page == "3" {
			n = 7
		}
		var fs []map[string]any
		for i := range n {
			fs = append(fs, map[string]any{"filename": fmt.Sprintf("p%s-%d", page, i), "status": "modified", "additions": 1, "deletions": 2, "previous_filename": "old"})
		}
		json.NewEncoder(w).Encode(fs)
	})
	files, err := c.Files(context.Background(), 9)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 207 || files[0].PreviousPath != "old" || files[206].Path != "p3-6" {
		t.Fatalf("got %d files", len(files))
	}
	if len(rec.reqs) != 3 || !strings.HasPrefix(rec.reqs[0], "GET /repos/o/r/pulls/9/files?per_page=100&page=1") {
		t.Fatalf("requests = %v", rec.reqs)
	}
}

func TestFilesStopsAtGitHubCap(t *testing.T) {
	t.Parallel()
	calls := 0
	c, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		var fs []map[string]any
		for i := 0; i < 100; i++ {
			fs = append(fs, map[string]any{"filename": fmt.Sprint(calls, "-", i)})
		}
		json.NewEncoder(w).Encode(fs)
	})
	files, err := c.Files(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3000 || calls != 30 {
		t.Fatalf("files %d calls %d", len(files), calls)
	}
}

func TestPullRequestBot(t *testing.T) {
	t.Parallel()
	c, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"number":4,"title":"t","body":"b","labels":[{"name":"bug"}],"user":{"login":"renovate[bot]","type":"Bot"},
		"base":{"ref":"main","sha":"s1"},"head":{"ref":"h","sha":"s2"},"changed_files":3}`)
	})
	pr, err := c.PullRequest(context.Background(), 4)
	if err != nil {
		t.Fatal(err)
	}
	if !pr.AuthorBot || pr.Labels[0] != "bug" || pr.BaseSHA != "s1" || pr.HeadSHA != "s2" || pr.ChangedFiles != 3 {
		t.Fatalf("pr = %+v", pr)
	}
}

func TestRemoveLabel(t *testing.T) {
	t.Parallel()
	status := http.StatusOK
	c, rec := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		io.WriteString(w, "[]")
	})
	if err := c.RemoveLabel(context.Background(), 5, "tier2 self/reviewed"); err != nil {
		t.Fatal(err)
	}
	if rec.reqs[0] != "DELETE /repos/o/r/issues/5/labels/tier2%20self%2Freviewed" {
		t.Fatalf("request = %s", rec.reqs[0])
	}
	status = http.StatusNotFound
	if err := c.RemoveLabel(context.Background(), 5, "x"); err != nil {
		t.Fatalf("404 should be ignored: %v", err)
	}
	status = http.StatusForbidden
	if err := c.RemoveLabel(context.Background(), 5, "x"); err == nil {
		t.Fatal("403 should fail")
	}
}

func TestLatestLabelEvent(t *testing.T) {
	t.Parallel()
	c, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `[
		 {"event":"labeled","created_at":"2027-01-05T09:00:00Z","actor":{"login":"old"},"label":{"name":"L"}},
		 {"event":"labeled","created_at":"2027-01-05T10:00:00Z","actor":{"login":"newest"},"label":{"name":"L"}},
		 {"event":"labeled","created_at":"2027-01-05T11:00:00Z","actor":{"login":"other"},"label":{"name":"M"}},
		 {"event":"unlabeled","created_at":"2027-01-05T12:00:00Z","actor":{"login":"x"},"label":{"name":"L"}},
		 {"event":"commented","created_at":"2027-01-05T13:00:00Z","actor":{"login":"y"}}]`)
	})
	ev, ok, err := c.LatestLabelEvent(context.Background(), 5, "L")
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if ev.Actor != "newest" || !ev.At.Equal(time.Date(2027, 1, 5, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("ev = %+v", ev)
	}
	if _, ok, _ := c.LatestLabelEvent(context.Background(), 5, "none"); ok {
		t.Fatal("found a label that was never applied")
	}
}

func TestAuthorCommits(t *testing.T) {
	t.Parallel()
	c, rec := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		n := 100
		if r.URL.Query().Get("page") == "2" {
			n = 12
		}
		var cs []map[string]string
		for i := 0; i < n; i++ {
			cs = append(cs, map[string]string{"sha": "x"})
		}
		json.NewEncoder(w).Encode(cs)
	})
	since := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	n, err := c.AuthorCommits(context.Background(), "alice", "main", since, 1000)
	if err != nil || n != 112 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if !strings.Contains(rec.reqs[0], "author=alice") || !strings.Contains(rec.reqs[0], "since=2026-05-01T00%3A00%3A00Z") || !strings.Contains(rec.reqs[0], "sha=main") {
		t.Fatalf("request = %s", rec.reqs[0])
	}
	if n, _ := c.AuthorCommits(context.Background(), "alice", "main", time.Time{}, 1); n != 1 {
		t.Fatalf("max not honoured: %d", n)
	}
	if strings.Contains(rec.reqs[len(rec.reqs)-1], "since=") {
		t.Fatalf("zero since must be omitted: %v", rec.reqs[len(rec.reqs)-1])
	}
}

func TestRecentMergedPRs(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	c, rec := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/repos/o/r/pulls/") && strings.HasSuffix(r.URL.Path, "/files"):
			json.NewEncoder(w).Encode([]map[string]string{{"filename": "a.go"}, {"filename": r.URL.Path}})
		default:
			json.NewEncoder(w).Encode([]map[string]any{
				{"number": 1, "merged_at": now.Add(-time.Hour)},
				{"number": 2, "merged_at": nil},
				{"number": 3, "merged_at": now.Add(-90 * 24 * time.Hour)},
				{"number": 4, "merged_at": now.Add(-2 * time.Hour)},
				{"number": 5, "merged_at": now.Add(-3 * time.Hour)},
			})
		}
	})
	got, err := c.RecentMergedPRs(context.Background(), "main", now.AddDate(0, 0, -30), 100, 4)
	if err != nil {
		t.Fatal(err)
	}
	if got.Examined != 2 || len(got.Files) != 2 || got.Capped {
		t.Fatalf("got %+v", got)
	}
	if !strings.Contains(rec.reqs[0], "state=closed") || !strings.Contains(rec.reqs[0], "base=main") || !strings.Contains(rec.reqs[0], "per_page=100") {
		t.Fatalf("list request = %s", rec.reqs[0])
	}
	for _, r := range rec.reqs {
		if strings.Contains(r, "/pulls/4/") || strings.Contains(r, "/pulls/2/") || strings.Contains(r, "/pulls/3/") {
			t.Errorf("fetched files for a skipped PR: %s", r)
		}
	}
	capped, _ := c.RecentMergedPRs(context.Background(), "main", now.AddDate(0, 0, -30), 5, 0)
	if !capped.Capped {
		t.Fatal("expected the cap flag when the page is full")
	}
}

func TestCreateCheckRunBody(t *testing.T) {
	t.Parallel()
	var body map[string]any
	c, rec := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		w.WriteHeader(http.StatusCreated)
		io.WriteString(w, "{}")
	})
	err := c.CreateCheckRun(context.Background(), CheckRun{
		Name: "risk-gate", HeadSHA: "abc", Conclusion: "success", Title: "T", Summary: "S", Text: "<!-- m -->\n```json\n{}\n```\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	if rec.reqs[0] != "POST /repos/o/r/check-runs" {
		t.Fatalf("request = %s", rec.reqs[0])
	}
	out := body["output"].(map[string]any)
	if body["name"] != "risk-gate" || body["head_sha"] != "abc" || body["status"] != "completed" || body["conclusion"] != "success" ||
		out["title"] != "T" || out["summary"] != "S" || out["text"] != "<!-- m -->\n```json\n{}\n```\n" {
		t.Fatalf("body = %v", body)
	}
}

func TestErrorsDoNotLeakToken(t *testing.T) {
	t.Parallel()
	c, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusInternalServerError) })
	c.Sleep = func(context.Context, time.Duration) error { return nil }
	_, err := c.PullRequest(context.Background(), 1)
	if err == nil || strings.Contains(err.Error(), "tok") {
		t.Fatalf("err = %v", err)
	}
}

func retryClient(t *testing.T, h http.HandlerFunc) (*Client, *[]time.Duration, *int) {
	t.Helper()
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	var waits []time.Duration
	c := New("o/r", "tok")
	c.BaseURL = srv.URL
	c.Sleep = func(_ context.Context, d time.Duration) error { waits = append(waits, d); return nil }
	return c, &waits, &calls
}

func TestRetries(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		respond   func(w http.ResponseWriter, call int)
		wantErr   bool
		wantCalls int
		wantWaits []time.Duration
	}{
		{"503 then ok", func(w http.ResponseWriter, call int) {
			if call == 1 {
				w.WriteHeader(503)
				return
			}
			io.WriteString(w, "{}")
		}, false, 2, []time.Duration{time.Second}},
		{"500 forever stops at three", func(w http.ResponseWriter, call int) { w.WriteHeader(500) }, true, 3, []time.Duration{time.Second, 2 * time.Second}},
		{"429 honours Retry-After", func(w http.ResponseWriter, call int) {
			if call == 1 {
				w.Header().Set("Retry-After", "7")
				w.WriteHeader(429)
				return
			}
			io.WriteString(w, "{}")
		}, false, 2, []time.Duration{7 * time.Second}},
		{"secondary rate limit 403", func(w http.ResponseWriter, call int) {
			if call == 1 {
				w.Header().Set("Retry-After", "3")
				w.WriteHeader(403)
				return
			}
			io.WriteString(w, "{}")
		}, false, 2, []time.Duration{3 * time.Second}},
		{"retry-after is capped", func(w http.ResponseWriter, call int) {
			if call == 1 {
				w.Header().Set("Retry-After", "100000")
				w.WriteHeader(429)
				return
			}
			io.WriteString(w, "{}")
		}, false, 2, []time.Duration{maxRetryWait}},
		{"plain 403 is final", func(w http.ResponseWriter, call int) { w.WriteHeader(403) }, true, 1, nil},
		{"404 is final", func(w http.ResponseWriter, call int) { w.WriteHeader(404) }, true, 1, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			n := 0
			c, waits, calls := retryClient(t, func(w http.ResponseWriter, r *http.Request) { n++; tc.respond(w, n) })
			_, err := c.PullRequest(context.Background(), 1)
			if (err != nil) != tc.wantErr || *calls != tc.wantCalls {
				t.Fatalf("err=%v calls=%d", err, *calls)
			}
			if fmt.Sprint(*waits) != fmt.Sprint(tc.wantWaits) {
				t.Fatalf("waits = %v, want %v", *waits, tc.wantWaits)
			}
		})
	}
}

func TestRetriesNetworkErrorsAndResendsBody(t *testing.T) {
	t.Parallel()
	var bodies []string
	n := 0
	c, _, _ := retryClient(t, func(w http.ResponseWriter, r *http.Request) {
		n++
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(b))
		if n == 1 {
			hj, _ := w.(http.Hijacker)
			conn, _, _ := hj.Hijack()
			conn.Close()
			return
		}
		w.WriteHeader(201)
		io.WriteString(w, "{}")
	})
	if err := c.CreateCheckRun(context.Background(), CheckRun{Name: "risk-gate", HeadSHA: "abc", Conclusion: "success"}); err != nil {
		t.Fatal(err)
	}
	if n != 2 || bodies[1] == "" || bodies[0] != bodies[1] {
		t.Fatalf("attempts %d bodies %q", n, bodies)
	}
}

func TestHeadCommitDateAndForcePush(t *testing.T) {
	t.Parallel()
	c, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/repos/o/r/commits/") {
			io.WriteString(w, `{"commit":{"committer":{"date":"2026-11-24T08:00:00Z"}}}`)
			return
		}
		io.WriteString(w, `[{"event":"head_ref_force_pushed","created_at":"2026-11-24T07:00:00Z"},
		 {"event":"head_ref_force_pushed","created_at":"2026-11-24T09:00:00Z"},{"event":"labeled","created_at":"2026-11-24T10:00:00Z"}]`)
	})
	d, err := c.HeadCommitDate(context.Background(), "abc")
	if err != nil || d.Hour() != 8 {
		t.Fatalf("d=%v err=%v", d, err)
	}
	f, ok, err := c.LatestForcePush(context.Background(), 5)
	if err != nil || !ok || f.Hour() != 9 {
		t.Fatalf("f=%v ok=%v err=%v", f, ok, err)
	}
}

func TestRetryBudgetIsSharedAndFailsFast(t *testing.T) {
	t.Parallel()
	calls := 0
	c, waits, _ := retryClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Retry-After", "50")
		w.WriteHeader(429)
	})
	// 50s per wait: 3 minutes buys three waits across calls, then no more.
	for i := 0; i < 4; i++ {
		if _, err := c.PullRequest(context.Background(), 1); err == nil {
			t.Fatal("expected an error")
		}
	}
	var total time.Duration
	for _, w := range *waits {
		total += w
	}
	if len(*waits) != 3 || total != 150*time.Second {
		t.Fatalf("waits = %v", *waits)
	}
	before := calls
	if _, err := c.PullRequest(context.Background(), 1); err == nil {
		t.Fatal("expected an error")
	}
	if calls != before+1 || len(*waits) != 3 {
		t.Fatalf("spent budget still retried: calls %d, waits %v", calls-before, *waits)
	}
	if err := c.CreateCheckRun(context.Background(), CheckRun{Name: "risk-gate"}); err == nil {
		t.Fatal("publish should fail once the budget is spent")
	}
}

func TestAddLabel(t *testing.T) {
	t.Parallel()
	var body string
	c, rec := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		io.WriteString(w, "[]")
	})
	if err := c.AddLabel(context.Background(), 5, "risk:tier-1"); err != nil {
		t.Fatal(err)
	}
	if rec.reqs[0] != "POST /repos/o/r/issues/5/labels" || strings.TrimSpace(body) != `{"labels":["risk:tier-1"]}` {
		t.Fatalf("request = %s body = %s", rec.reqs[0], body)
	}
}

func TestPullRequestFork(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		head string
		want bool
	}{
		"same repo":    {`{"ref":"a","sha":"s","repo":{"full_name":"o/r"}}`, false},
		"fork":         {`{"ref":"a","sha":"s","repo":{"full_name":"x/r"}}`, true},
		"deleted fork": {`{"ref":"a","sha":"s","repo":null}`, true},
	} {
		c, _ := newClient(t, func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `{"number":1,"user":{"login":"a"},"base":{"ref":"main","sha":"b","repo":{"full_name":"o/r"}},"head":`+tc.head+`}`)
		})
		pr, err := c.PullRequest(context.Background(), 1)
		if err != nil || pr.Fork != tc.want {
			t.Errorf("%s: fork = %v, err = %v", name, pr.Fork, err)
		}
	}
}
