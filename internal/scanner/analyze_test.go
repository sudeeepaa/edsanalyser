package scanner

import (
	"net/url"
	"strings"
	"testing"
)

func TestNormalizeInputURL(t *testing.T) {
	parsed, err := NormalizeInputURL("example.com/docs#intro")
	if err != nil {
		t.Fatalf("NormalizeInputURL returned error: %v", err)
	}
	if parsed.String() != "https://example.com/docs" {
		t.Fatalf("unexpected normalized URL: %s", parsed.String())
	}

	if _, err := NormalizeInputURL("ftp://example.com"); err == nil {
		t.Fatal("expected invalid scheme error")
	}
}

func TestParseSitemapXML(t *testing.T) {
	pages, sitemaps := ParseSitemapXML([]byte(`
<sitemapindex>
  <sitemap><loc>https://example.com/sitemap-pages.xml</loc></sitemap>
</sitemapindex>`))
	if len(pages) != 0 {
		t.Fatalf("expected no pages, got %v", pages)
	}
	if len(sitemaps) != 1 || sitemaps[0] != "https://example.com/sitemap-pages.xml" {
		t.Fatalf("unexpected sitemaps: %v", sitemaps)
	}

	pages, _ = ParseSitemapXML([]byte(`
<urlset>
  <url><loc>https://example.com/</loc></url>
  <url><loc>https://example.com/about</loc></url>
</urlset>`))
	if len(pages) != 2 {
		t.Fatalf("expected two pages, got %v", pages)
	}
}

func TestParseDiscoveryJSON(t *testing.T) {
	root, _ := url.Parse("https://example.com/")
	pages := ParseDiscoveryJSON([]byte(`{"data":[{"path":"/about"},{"url":"https://example.com/products"}]}`), root)
	if len(pages) != 2 {
		t.Fatalf("expected two pages, got %v", pages)
	}
}

func TestAnalyzeHTMLExtractsEDSSEOAndLinks(t *testing.T) {
	root, _ := url.Parse("https://example.com/")
	html := `
<!doctype html>
<html lang="en">
  <head>
    <title>Home | Example</title>
    <link rel="canonical" href="https://example.com/" />
    <meta name="description" content="A useful page" />
    <meta property="og:title" content="OG Home" />
    <meta property="og:image" content="https://example.com/og.png" />
    <meta property="og:url" content="https://example.com/" />
  </head>
  <body>
    <main>
      <div class="section hero-area">
        <div class="hero spotlight"><h1>Welcome</h1></div>
        <div class="section-metadata"><div><div>Style</div><div>dark wide</div></div></div>
      </div>
      <div>
        <div class="cards three-up"></div>
      </div>
    </main>
    <a href="/about">About</a>
    <a href="https://adobe.com">Adobe</a>
    <a href="mailto:hello@example.com">Email</a>
  </body>
</html>`
	page, err := AnalyzeHTML("https://example.com/", strings.NewReader(html), root)
	if err != nil {
		t.Fatalf("AnalyzeHTML returned error: %v", err)
	}
	if page.Title != "Home | Example" || page.H1 != "Welcome" || page.OG.Title != "OG Home" {
		t.Fatalf("metadata was not extracted: %+v", page)
	}
	if page.SectionCount != 2 || page.BlockCount != 2 {
		t.Fatalf("unexpected EDS counts: sections=%d blocks=%d", page.SectionCount, page.BlockCount)
	}
	if page.InternalLinks != 1 || page.ExternalLinks != 1 || page.LinkCount != 3 {
		t.Fatalf("unexpected link counts: %+v", page.Links)
	}
	if !contains(page.Sections[0].Variations, "dark") || !contains(page.Sections[0].Variations, "wide") {
		t.Fatalf("section metadata variations missing: %+v", page.Sections[0].Variations)
	}
}

func TestAnalyzeHTMLResolvesRelativeLinksAgainstCurrentPage(t *testing.T) {
	root, _ := url.Parse("https://example.com/")
	page, err := AnalyzeHTML("https://example.com/docs/index", strings.NewReader(`<a href="child">Child</a>`), root)
	if err != nil {
		t.Fatalf("AnalyzeHTML returned error: %v", err)
	}
	if len(page.Links) != 1 {
		t.Fatalf("expected one link, got %+v", page.Links)
	}
	if page.Links[0].URL != "https://example.com/docs/child" {
		t.Fatalf("relative link resolved incorrectly: %s", page.Links[0].URL)
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
