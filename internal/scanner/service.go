package scanner

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type ServiceOptions struct {
	HTTPClient *http.Client
	Lighthouse LighthouseRunner
	Workers    int
}

type ScanOptions struct {
	CrawlLimit      *int
	LighthouseMode  string
	LighthouseLimit int
}

func DefaultScanOptions() ScanOptions {
	return ScanOptions{
		LighthouseMode:  "top",
		LighthouseLimit: 5,
	}
}

type Service struct {
	store      Store
	client     *http.Client
	lighthouse LighthouseRunner
	workers    int

	mu      sync.Mutex
	cancels map[string]context.CancelFunc
	events  map[string][]chan Event
}

func NewService(store Store, opts ServiceOptions) *Service {
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 25 * time.Second}
	}
	lighthouse := opts.Lighthouse
	if lighthouse == nil {
		lighthouse = NoopLighthouseRunner{}
	}
	workers := opts.Workers
	if workers <= 0 {
		workers = 4
	}
	return &Service{
		store:      store,
		client:     client,
		lighthouse: lighthouse,
		workers:    workers,
		cancels:    map[string]context.CancelFunc{},
		events:     map[string][]chan Event{},
	}
}

func (s *Service) StartScan(parent context.Context, inputURL string, opts ScanOptions) (ScanSummary, error) {
	root, err := NormalizeInputURL(inputURL)
	if err != nil {
		return ScanSummary{}, err
	}
	if opts.CrawlLimit != nil && *opts.CrawlLimit <= 0 {
		opts.CrawlLimit = nil
	}
	if opts.LighthouseMode == "" {
		opts.LighthouseMode = "top"
	}
	if opts.LighthouseLimit <= 0 {
		opts.LighthouseLimit = 5
	}

	id := newID()
	ctx, cancel := context.WithCancel(context.Background())
	scan := ScanSummary{
		ID:        id,
		InputURL:  inputURL,
		RootURL:   root.String(),
		Status:    "running",
		Phase:     "discovering",
		StartedAt: time.Now(),
	}
	if err := s.store.CreateScan(scan); err != nil {
		cancel()
		return ScanSummary{}, err
	}

	s.mu.Lock()
	s.cancels[id] = cancel
	s.mu.Unlock()

	go s.runScan(ctx, scan, root, opts)
	return scan, parent.Err()
}

func (s *Service) ListScans() ([]ScanSummary, error) {
	return s.store.ListScans()
}

func (s *Service) GetScan(id string) (ScanResult, error) {
	return s.store.GetScan(id)
}

func (s *Service) CancelScan(id string) error {
	s.mu.Lock()
	cancel := s.cancels[id]
	s.mu.Unlock()
	if cancel == nil {
		return errors.New("scan is not running")
	}
	cancel()
	s.publish(Event{Type: "cancel", ScanID: id, Message: "Scan cancellation requested"})
	return nil
}

func (s *Service) Subscribe(scanID string) (<-chan Event, func()) {
	ch := make(chan Event, 32)
	s.mu.Lock()
	s.events[scanID] = append(s.events[scanID], ch)
	s.mu.Unlock()
	cancel := func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		subscribers := s.events[scanID]
		for i, candidate := range subscribers {
			if candidate == ch {
				s.events[scanID] = append(subscribers[:i], subscribers[i+1:]...)
				close(ch)
				return
			}
		}
	}
	return ch, cancel
}

func (s *Service) runScan(ctx context.Context, scan ScanSummary, root *url.URL, opts ScanOptions) {
	defer func() {
		s.mu.Lock()
		delete(s.cancels, scan.ID)
		s.mu.Unlock()
	}()

	s.publish(Event{Type: "start", ScanID: scan.ID, Message: "Scan started"})
	discoverer := Discoverer{Client: s.client}
	seeds, err := discoverer.Discover(ctx, root)
	if err != nil {
		s.publish(Event{Type: "warning", ScanID: scan.ID, Message: "Discovery failed; scanning the entered URL"})
		seeds = []string{root.String()}
	}
	if len(seeds) == 0 {
		seeds = []string{root.String()}
	}

	seen := map[string]bool{}
	var queue []string
	limit := 0
	if opts.CrawlLimit != nil {
		limit = *opts.CrawlLimit
	}
	enqueue := func(raw string) {
		if limit > 0 && len(seen) >= limit {
			return
		}
		normalized, ok := normalizePageURL(raw, root)
		if !ok || seen[normalized] {
			return
		}
		parsed, err := url.Parse(normalized)
		if err != nil || !sameOrigin(parsed, root) {
			return
		}
		seen[normalized] = true
		queue = append(queue, normalized)
		scan.DiscoveredPages = len(seen)
		_ = s.store.UpdateScan(scan)
	}
	for _, seed := range seeds {
		enqueue(seed)
	}
	scan.Phase = "analyzing"
	_ = s.store.UpdateScan(scan)
	s.publish(Event{Type: "discovered", ScanID: scan.ID, Message: fmt.Sprintf("%d pages queued", len(queue)), Data: scan})

	jobs := make(chan string)
	results := make(chan PageResult)
	for i := 0; i < s.workers; i++ {
		go func() {
			for pageURL := range jobs {
				page := s.fetchAndAnalyzePage(ctx, pageURL, root)
				select {
				case results <- page:
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	inFlight := 0
	analyzedPages := []PageResult{}
	for len(queue) > 0 || inFlight > 0 {
		if ctx.Err() != nil {
			s.cancelScan(scan, jobs)
			close(jobs)
			return
		}

		var next string
		var outbound chan<- string
		if len(queue) > 0 {
			next = queue[0]
			outbound = jobs
		}

		select {
		case outbound <- next:
			queue = queue[1:]
			inFlight++
			s.publish(Event{Type: "page-start", ScanID: scan.ID, PageURL: next})
		case page := <-results:
			inFlight--
			scan.CompletedPages++
			scan.FastCompletedPages = scan.CompletedPages
			if page.FetchError != "" {
				scan.FailedPages++
			}
			page.AuditStatus = "pending"
			page = NormalizePage(page)
			analyzedPages = append(analyzedPages, page)
			_ = s.store.SavePage(scan.ID, page)
			_ = s.store.UpdateScan(scan)
			s.publish(Event{Type: "page-analyzed", ScanID: scan.ID, PageURL: page.URL, Data: page})
			for _, link := range page.Links {
				if link.Kind == "internal" {
					enqueue(link.URL)
				}
			}
		case <-ctx.Done():
			s.cancelScan(scan, jobs)
			close(jobs)
			return
		}
	}

	close(jobs)
	scan.Phase = "fast-complete"
	_ = s.store.UpdateScan(scan)
	s.publish(Event{Type: "fast-complete", ScanID: scan.ID, Message: "Fast report ready", Data: scan})

	scan = s.runLighthouseAudits(ctx, scan, analyzedPages, opts)
	if ctx.Err() != nil {
		scan.Status = "cancelled"
		scan.Phase = "cancelled"
		scan.FinishedAt = time.Now()
		scan.Error = "scan cancelled"
		_ = s.store.UpdateScan(scan)
		s.publish(Event{Type: "complete", ScanID: scan.ID, Message: "Scan cancelled", Data: scan})
		return
	}
	scan.Status = "completed"
	scan.Phase = "completed"
	scan.FinishedAt = time.Now()
	_ = s.store.UpdateScan(scan)
	s.publish(Event{Type: "complete", ScanID: scan.ID, Message: "Scan completed", Data: scan})
}

func (s *Service) cancelScan(scan ScanSummary, jobs chan string) {
	scan.Status = "cancelled"
	scan.Phase = "cancelled"
	scan.FinishedAt = time.Now()
	scan.Error = "scan cancelled"
	_ = s.store.UpdateScan(scan)
	s.publish(Event{Type: "complete", ScanID: scan.ID, Message: "Scan cancelled", Data: scan})
}

func (s *Service) runLighthouseAudits(ctx context.Context, scan ScanSummary, pages []PageResult, opts ScanOptions) ScanSummary {
	auditPages := selectAuditPages(pages, opts)
	scan.AuditQueuedPages = len(auditPages)
	if scan.AuditQueuedPages == 0 {
		return scan
	}
	scan.Phase = "auditing"
	_ = s.store.UpdateScan(scan)

	rollup := newScoreRollup()
	for _, page := range auditPages {
		if ctx.Err() != nil {
			return scan
		}
		page.AuditStatus = "running"
		page.AuditError = ""
		_ = s.store.SavePage(scan.ID, page)
		s.publish(Event{Type: "audit-start", ScanID: scan.ID, PageURL: page.URL, Data: page})

		audited := s.auditPageWithLighthouse(ctx, page)
		if ctx.Err() != nil {
			return scan
		}
		if audited.AuditStatus == "failed" {
			scan.AuditFailedPages++
			s.publish(Event{Type: "audit-error", ScanID: scan.ID, PageURL: audited.URL, Data: audited})
		} else {
			scan.AuditCompletedPages++
			rollup.Add(audited.Lighthouse)
			scan.Scores = rollup.ScoreSet()
			s.publish(Event{Type: "audit-complete", ScanID: scan.ID, PageURL: audited.URL, Data: audited})
		}
		_ = s.store.SavePage(scan.ID, audited)
		_ = s.store.UpdateScan(scan)
	}
	return scan
}

func selectAuditPages(pages []PageResult, opts ScanOptions) []PageResult {
	if opts.LighthouseMode == "none" {
		return []PageResult{}
	}
	limit := opts.LighthouseLimit
	if limit <= 0 {
		limit = 5
	}
	selected := make([]PageResult, 0, limit)
	for _, page := range pages {
		if page.FetchError != "" {
			continue
		}
		selected = append(selected, page)
		if opts.LighthouseMode == "top" && len(selected) >= limit {
			break
		}
	}
	return selected
}

func (s *Service) fetchAndAnalyzePage(ctx context.Context, pageURL string, root *url.URL) PageResult {
	page := PageResult{URL: pageURL}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		page.FetchError = err.Error()
		return page
	}
	req.Header.Set("User-Agent", "EDSAnalyser/0.1 (+https://localhost)")
	resp, err := s.client.Do(req)
	if err != nil {
		page.FetchError = err.Error()
		return page
	}
	defer resp.Body.Close()
	page.StatusCode = resp.StatusCode
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		page.FetchError = fmt.Sprintf("HTTP %d", resp.StatusCode)
		return page
	}

	analyzed, err := AnalyzeHTML(pageURL, io.LimitReader(resp.Body, 16*1024*1024), root)
	if err != nil {
		page.FetchError = err.Error()
		return page
	}
	analyzed.StatusCode = resp.StatusCode
	for i := range analyzed.Links {
		analyzed.Links[i].PageURL = pageURL
	}
	analyzed.AuditStatus = "pending"
	return NormalizePage(analyzed)
}

func (s *Service) auditPageWithLighthouse(ctx context.Context, page PageResult) PageResult {
	scores, err := s.lighthouse.Audit(ctx, page.URL)
	if err != nil {
		page.AuditError = err.Error()
		page.AuditStatus = "failed"
	} else {
		page.Lighthouse = scores
		page.AuditStatus = "complete"
		page.AuditError = ""
	}
	return NormalizePage(page)
}

func (s *Service) publish(event Event) {
	event.Timestamp = time.Now()
	s.mu.Lock()
	subscribers := append([]chan Event(nil), s.events[event.ScanID]...)
	s.mu.Unlock()
	for _, ch := range subscribers {
		select {
		case ch <- event:
		default:
		}
	}
}

func newID() string {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(bytes[:])
}

type scoreRollup struct {
	performance        float64
	performanceCount   int
	accessibility      float64
	accessibilityCount int
	bestPractices      float64
	bestPracticesCount int
	seo                float64
	seoCount           int
	health             float64
	healthCount        int
}

func newScoreRollup() *scoreRollup {
	return &scoreRollup{}
}

func (r *scoreRollup) Add(scores ScoreSet) {
	add := func(value *float64, sum *float64, count *int) {
		if value == nil {
			return
		}
		*sum += *value
		*count = *count + 1
	}
	add(scores.Performance, &r.performance, &r.performanceCount)
	add(scores.Accessibility, &r.accessibility, &r.accessibilityCount)
	add(scores.BestPractices, &r.bestPractices, &r.bestPracticesCount)
	add(scores.SEO, &r.seo, &r.seoCount)
	add(scores.Health, &r.health, &r.healthCount)
}

func (r *scoreRollup) ScoreSet() ScoreSet {
	return ScoreSet{
		Performance:   average(r.performance, r.performanceCount),
		Accessibility: average(r.accessibility, r.accessibilityCount),
		BestPractices: average(r.bestPractices, r.bestPracticesCount),
		SEO:           average(r.seo, r.seoCount),
		Health:        average(r.health, r.healthCount),
	}
}

func average(sum float64, count int) *float64 {
	if count == 0 {
		return nil
	}
	value := sum / float64(count)
	return &value
}

func HasLighthouseError(err string) bool {
	return strings.TrimSpace(err) != ""
}
