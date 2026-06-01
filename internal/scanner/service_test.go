package scanner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestServiceScansFixtureSiteFromSitemap(t *testing.T) {
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	defer server.Close()

	mux.HandleFunc("/sitemap.xml", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `<urlset><url><loc>%s/</loc></url><url><loc>%s/about</loc></url></urlset>`, server.URL, server.URL)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, pageHTML("Home", "/about"))
	})
	mux.HandleFunc("/about", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, pageHTML("About", "/"))
	})

	store := openTestStore(t)
	defer store.Close()
	service := NewService(store, ServiceOptions{
		HTTPClient: server.Client(),
		Lighthouse: fixedLighthouse{},
		Workers:    2,
	})

	scan, err := service.StartScan(context.Background(), server.URL, DefaultScanOptions())
	if err != nil {
		t.Fatalf("StartScan returned error: %v", err)
	}
	result := waitForScan(t, service, scan.ID, "completed")
	if result.Summary.CompletedPages != 2 {
		t.Fatalf("expected two completed pages, got %+v", result.Summary)
	}
	if len(result.Pages) != 2 || len(result.Blocks) == 0 {
		t.Fatalf("expected persisted page and block details, got %+v", result)
	}
	if result.Summary.Scores.Health == nil || *result.Summary.Scores.Health != 91 {
		t.Fatalf("unexpected health score: %+v", result.Summary.Scores)
	}
}

func TestServiceCancelsSlowScan(t *testing.T) {
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	defer server.Close()
	mux.HandleFunc("/sitemap.xml", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `<urlset><url><loc>%s/slow</loc></url></urlset>`, server.URL)
	})
	mux.HandleFunc("/slow", func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(2 * time.Second):
			fmt.Fprint(w, pageHTML("Slow", "/slow"))
		case <-r.Context().Done():
		}
	})

	store := openTestStore(t)
	defer store.Close()
	service := NewService(store, ServiceOptions{
		HTTPClient: server.Client(),
		Lighthouse: fixedLighthouse{},
		Workers:    1,
	})

	scan, err := service.StartScan(context.Background(), server.URL, DefaultScanOptions())
	if err != nil {
		t.Fatalf("StartScan returned error: %v", err)
	}
	if err := service.CancelScan(scan.ID); err != nil {
		t.Fatalf("CancelScan returned error: %v", err)
	}
	result := waitForScan(t, service, scan.ID, "cancelled")
	if result.Summary.Status != "cancelled" {
		t.Fatalf("expected cancelled scan, got %s", result.Summary.Status)
	}
}

func TestFastAnalysisPersistsBeforeLighthouseFinishes(t *testing.T) {
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	defer server.Close()
	mux.HandleFunc("/sitemap.xml", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `<urlset><url><loc>%s/</loc></url></urlset>`, server.URL)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, pageHTML("Home", "/"))
	})

	store := openTestStore(t)
	defer store.Close()
	runner := &blockingLighthouse{started: make(chan string, 1), release: make(chan struct{})}
	service := NewService(store, ServiceOptions{
		HTTPClient: server.Client(),
		Lighthouse: runner,
		Workers:    1,
	})

	scan, err := service.StartScan(context.Background(), server.URL, DefaultScanOptions())
	if err != nil {
		t.Fatalf("StartScan returned error: %v", err)
	}
	select {
	case <-runner.started:
	case <-time.After(5 * time.Second):
		t.Fatal("lighthouse did not start")
	}

	result, err := service.GetScan(scan.ID)
	if err != nil {
		t.Fatalf("GetScan returned error: %v", err)
	}
	if result.Summary.FastCompletedPages != 1 || len(result.Pages) != 1 {
		t.Fatalf("expected fast page results before lighthouse completion, got %+v", result)
	}
	if result.Pages[0].AuditStatus != "running" {
		t.Fatalf("expected running audit status, got %s", result.Pages[0].AuditStatus)
	}
	close(runner.release)
	waitForScan(t, service, scan.ID, "completed")
}

func TestDefaultLighthouseLimitAuditsTopFivePages(t *testing.T) {
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	defer server.Close()
	mux.HandleFunc("/sitemap.xml", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `<urlset>`)
		for i := 0; i < 7; i++ {
			fmt.Fprintf(w, `<url><loc>%s/page-%d</loc></url>`, server.URL, i)
		}
		fmt.Fprint(w, `</urlset>`)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, pageHTML(r.URL.Path, "/page-0"))
	})

	store := openTestStore(t)
	defer store.Close()
	runner := &countingLighthouse{}
	service := NewService(store, ServiceOptions{
		HTTPClient: server.Client(),
		Lighthouse: runner,
		Workers:    3,
	})

	scan, err := service.StartScan(context.Background(), server.URL, DefaultScanOptions())
	if err != nil {
		t.Fatalf("StartScan returned error: %v", err)
	}
	result := waitForScan(t, service, scan.ID, "completed")
	if got := runner.count.Load(); got != 5 {
		t.Fatalf("expected 5 lighthouse audits, got %d", got)
	}
	if result.Summary.AuditQueuedPages != 5 || result.Summary.AuditCompletedPages != 5 {
		t.Fatalf("unexpected audit counters: %+v", result.Summary)
	}
}

func TestLighthouseFailureDoesNotIncrementPageFailures(t *testing.T) {
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	defer server.Close()
	mux.HandleFunc("/sitemap.xml", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `<urlset><url><loc>%s/</loc></url></urlset>`, server.URL)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, pageHTML("Home", "/"))
	})

	store := openTestStore(t)
	defer store.Close()
	service := NewService(store, ServiceOptions{
		HTTPClient: server.Client(),
		Lighthouse: failingLighthouse{},
		Workers:    1,
	})

	scan, err := service.StartScan(context.Background(), server.URL, DefaultScanOptions())
	if err != nil {
		t.Fatalf("StartScan returned error: %v", err)
	}
	result := waitForScan(t, service, scan.ID, "completed")
	if result.Summary.FailedPages != 0 {
		t.Fatalf("lighthouse failure should not count as page failure: %+v", result.Summary)
	}
	if result.Summary.AuditFailedPages != 1 {
		t.Fatalf("expected one audit failure: %+v", result.Summary)
	}
}

func TestStoreNormalizesNullJSONFields(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()
	scan := ScanSummary{
		ID:        "scan-null-json",
		InputURL:  "https://example.com",
		RootURL:   "https://example.com/",
		Status:    "completed",
		Phase:     "completed",
		StartedAt: time.Now(),
	}
	if err := store.CreateScan(scan); err != nil {
		t.Fatalf("CreateScan returned error: %v", err)
	}
	_, err := store.db.Exec(`
INSERT INTO pages (
  scan_id, url, status_code, title, h1, canonical, description, robots, lang,
  og_json, links_json, blocks_json, sections_json, block_count, section_count, link_count,
  internal_links, external_links, audit_status
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		scan.ID, "https://example.com/", 200, "Home", "", "", "", "", "",
		`{}`, `null`, `null`, `null`, 0, 0, 0, 0, 0, "")
	if err != nil {
		t.Fatalf("insert null page returned error: %v", err)
	}

	result, err := store.GetScan(scan.ID)
	if err != nil {
		t.Fatalf("GetScan returned error: %v", err)
	}
	if len(result.Pages) != 1 || result.Pages[0].Links == nil || result.Pages[0].Blocks == nil || result.Pages[0].Sections == nil {
		t.Fatalf("page fields were not normalized: %+v", result.Pages)
	}
	payload, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("json marshal returned error: %v", err)
	}
	for _, forbidden := range []string{`"pages":null`, `"links":null`, `"blocks":null`, `"sections":null`, `"variations":null`} {
		if strings.Contains(string(payload), forbidden) {
			t.Fatalf("response still contains %s: %s", forbidden, payload)
		}
	}
}

func openTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "test.sqlite"))
	if err != nil {
		t.Fatalf("OpenSQLiteStore returned error: %v", err)
	}
	return store
}

func waitForScan(t *testing.T, service *Service, id string, status string) ScanResult {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var last ScanResult
	for time.Now().Before(deadline) {
		result, err := service.GetScan(id)
		if err == nil {
			last = result
			if result.Summary.Status == status {
				return result
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("scan did not reach %q; last result: %+v", status, last.Summary)
	return ScanResult{}
}

func pageHTML(title, link string) string {
	return fmt.Sprintf(`
<!doctype html>
<html lang="en">
  <head>
    <title>%s</title>
    <meta name="description" content="%s description" />
    <meta property="og:title" content="%s" />
    <meta property="og:image" content="/og.png" />
    <meta property="og:url" content="/" />
  </head>
  <body>
    <main>
      <div class="section default"><div class="hero primary"><h1>%s</h1></div></div>
    </main>
    <a href="%s">Next</a>
  </body>
</html>`, title, title, title, title, link)
}

type fixedLighthouse struct{}

func (fixedLighthouse) Audit(context.Context, string) (ScoreSet, error) {
	performance := 90.0
	accessibility := 92.0
	bestPractices := 88.0
	seo := 94.0
	health := 91.0
	return ScoreSet{
		Performance:   &performance,
		Accessibility: &accessibility,
		BestPractices: &bestPractices,
		SEO:           &seo,
		Health:        &health,
	}, nil
}

type blockingLighthouse struct {
	started chan string
	release chan struct{}
}

func (b *blockingLighthouse) Audit(ctx context.Context, pageURL string) (ScoreSet, error) {
	b.started <- pageURL
	select {
	case <-b.release:
		return fixedLighthouse{}.Audit(ctx, pageURL)
	case <-ctx.Done():
		return ScoreSet{}, ctx.Err()
	}
}

type countingLighthouse struct {
	count atomic.Int32
}

func (c *countingLighthouse) Audit(ctx context.Context, pageURL string) (ScoreSet, error) {
	c.count.Add(1)
	return fixedLighthouse{}.Audit(ctx, pageURL)
}

type failingLighthouse struct{}

func (failingLighthouse) Audit(context.Context, string) (ScoreSet, error) {
	return ScoreSet{}, errors.New("lighthouse failed")
}
