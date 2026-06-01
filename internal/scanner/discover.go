package scanner

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Discoverer struct {
	Client *http.Client
}

func (d Discoverer) Discover(ctx context.Context, start *url.URL) ([]string, error) {
	client := d.Client
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}

	seen := map[string]bool{}
	var pages []string
	add := func(raw string) {
		normalized, ok := normalizePageURL(raw, start)
		if !ok {
			return
		}
		parsed, err := url.Parse(normalized)
		if err != nil || !sameOrigin(parsed, start) || seen[normalized] {
			return
		}
		seen[normalized] = true
		pages = append(pages, normalized)
	}

	for _, path := range []string{"/sitemap.xml", "/sitemap.json", "/query-index.json"} {
		endpoint := *start
		endpoint.Path = path
		endpoint.RawQuery = ""
		endpoint.Fragment = ""

		found, err := d.fetchDiscoveryEndpoint(ctx, client, endpoint.String(), start)
		if err == nil {
			for _, page := range found {
				add(page)
			}
		}
	}

	add(start.String())
	if len(pages) == 1 {
		fallback, err := d.linksFromPage(ctx, client, start.String(), start)
		if err == nil {
			for _, page := range fallback {
				add(page)
			}
		}
	}
	return pages, nil
}

func (d Discoverer) fetchDiscoveryEndpoint(ctx context.Context, client *http.Client, endpoint string, root *url.URL) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("discovery endpoint %s returned %d", endpoint, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16*1024*1024))
	if err != nil {
		return nil, err
	}

	if strings.HasSuffix(endpoint, ".xml") {
		pages, childSitemaps := ParseSitemapXML(body)
		for _, child := range childSitemaps {
			childURL, ok := normalizePageURL(child, root)
			if !ok {
				continue
			}
			parsed, err := url.Parse(childURL)
			if err != nil || !sameOrigin(parsed, root) {
				continue
			}
			childPages, err := d.fetchDiscoveryEndpoint(ctx, client, childURL, root)
			if err == nil {
				pages = append(pages, childPages...)
			}
		}
		return pages, nil
	}
	return ParseDiscoveryJSON(body, root), nil
}

func (d Discoverer) linksFromPage(ctx context.Context, client *http.Client, pageURL string, root *url.URL) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("fallback page returned %d", resp.StatusCode)
	}
	page, err := AnalyzeHTML(pageURL, io.LimitReader(resp.Body, 16*1024*1024), root)
	if err != nil {
		return nil, err
	}
	var urls []string
	for _, link := range page.Links {
		if link.Kind == "internal" {
			urls = append(urls, link.URL)
		}
	}
	return urls, nil
}

func ParseSitemapXML(body []byte) ([]string, []string) {
	type loc struct {
		Loc string `xml:"loc"`
	}
	type urlSet struct {
		URLs []loc `xml:"url"`
	}
	type sitemapIndex struct {
		Sitemaps []loc `xml:"sitemap"`
	}

	var urls urlSet
	var pages []string
	if err := xml.Unmarshal(body, &urls); err == nil {
		for _, entry := range urls.URLs {
			if strings.TrimSpace(entry.Loc) != "" {
				pages = append(pages, strings.TrimSpace(entry.Loc))
			}
		}
	}

	var index sitemapIndex
	var sitemaps []string
	if err := xml.Unmarshal(body, &index); err == nil {
		for _, entry := range index.Sitemaps {
			if strings.TrimSpace(entry.Loc) != "" {
				sitemaps = append(sitemaps, strings.TrimSpace(entry.Loc))
			}
		}
	}
	return pages, sitemaps
}

func ParseDiscoveryJSON(body []byte, root *url.URL) []string {
	var value any
	if err := json.Unmarshal(body, &value); err != nil {
		return nil
	}

	var pages []string
	var walk func(any, string)
	walk = func(v any, key string) {
		switch node := v.(type) {
		case []any:
			for _, item := range node {
				walk(item, key)
			}
		case map[string]any:
			for k, item := range node {
				walk(item, strings.ToLower(k))
			}
		case string:
			if key == "path" || key == "url" || key == "loc" || strings.Contains(key, "url") {
				if normalized, ok := normalizePageURL(node, root); ok {
					pages = append(pages, normalized)
				}
			}
		}
	}
	walk(value, "")
	return pages
}
