package scanner

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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

	scan, err := service.StartScan(context.Background(), server.URL, nil)
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

	scan, err := service.StartScan(context.Background(), server.URL, nil)
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
