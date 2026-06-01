import { useEffect, useMemo, useState } from 'react';
import {
  Activity,
  BarChart3,
  Boxes,
  ExternalLink,
  FileSearch,
  Globe2,
  History,
  Link2,
  Loader2,
  OctagonX,
  Play,
  Search,
  ShieldCheck,
  StopCircle,
} from 'lucide-react';
import { cancelScan, getScan, listScans, startScan } from './api';
import type { BlockStat, PageResult, ScanEvent, ScanResult, ScanSummary, SectionStat } from './types';

type Tab = 'overview' | 'pages' | 'blocks' | 'links' | 'seo' | 'history';

const tabs: Array<{ id: Tab; label: string; icon: typeof Activity }> = [
  { id: 'overview', label: 'Overview', icon: Activity },
  { id: 'pages', label: 'Pages', icon: FileSearch },
  { id: 'blocks', label: 'Blocks', icon: Boxes },
  { id: 'links', label: 'Links', icon: Link2 },
  { id: 'seo', label: 'SEO / OG', icon: ShieldCheck },
  { id: 'history', label: 'History', icon: History },
];

export default function App() {
  const [url, setUrl] = useState('');
  const [scan, setScan] = useState<ScanResult | null>(null);
  const [history, setHistory] = useState<ScanSummary[]>([]);
  const [activeScan, setActiveScan] = useState<ScanSummary | null>(null);
  const [tab, setTab] = useState<Tab>('overview');
  const [pageFilter, setPageFilter] = useState('');
  const [selectedPageURL, setSelectedPageURL] = useState<string | null>(null);
  const [events, setEvents] = useState<ScanEvent[]>([]);
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    void refreshHistory();
  }, []);

  useEffect(() => {
    if (!activeScan || activeScan.status !== 'running') {
      return undefined;
    }
    const source = new EventSource(`/api/scans/${activeScan.id}/events`);
    const eventNames = ['start', 'discovered', 'warning', 'page-start', 'page-complete', 'cancel', 'complete'];
    const handleEvent = (message: MessageEvent) => {
      const parsed = JSON.parse(message.data) as ScanEvent;
      setEvents((current) => [parsed, ...current].slice(0, 8));
      if (parsed.type === 'page-complete' || parsed.type === 'complete' || parsed.type === 'discovered') {
        void loadScan(activeScan.id);
      }
      if (parsed.type === 'complete') {
        void refreshHistory();
        source.close();
      }
    };
    eventNames.forEach((name) => source.addEventListener(name, handleEvent));
    source.onerror = () => {
      source.close();
    };
    return () => {
      eventNames.forEach((name) => source.removeEventListener(name, handleEvent));
      source.close();
    };
  }, [activeScan?.id, activeScan?.status]);

  const selectedPage = useMemo(() => {
    if (!scan?.pages.length) {
      return null;
    }
    return scan.pages.find((page) => page.url === selectedPageURL) || scan.pages[0];
  }, [scan, selectedPageURL]);

  const filteredPages = useMemo(() => {
    const value = pageFilter.trim().toLowerCase();
    if (!scan) {
      return [];
    }
    if (!value) {
      return scan.pages;
    }
    return scan.pages.filter((page) =>
      [page.url, page.title, page.h1, page.description, page.auditError, page.fetchError]
        .filter((item): item is string => Boolean(item))
        .some((item) => item.toLowerCase().includes(value)),
    );
  }, [scan, pageFilter]);

  async function refreshHistory() {
    try {
      setHistory(await listScans());
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Unable to load history');
    }
  }

  async function loadScan(id: string) {
    try {
      const result = await getScan(id);
      setScan(result);
      setActiveScan(result.summary);
      setSelectedPageURL((current) => current || result.pages[0]?.url || null);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Unable to load scan');
    }
  }

  async function onStartScan(event: React.FormEvent) {
    event.preventDefault();
    setError('');
    setEvents([]);
    setLoading(true);
    try {
      const created = await startScan(url, null);
      setActiveScan(created);
      setScan({ summary: created, pages: [], blocks: [], sections: [], links: emptyLinks, seo: emptySEO, generatedAt: new Date().toISOString() });
      setSelectedPageURL(null);
      setTab('overview');
      await refreshHistory();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Unable to start scan');
    } finally {
      setLoading(false);
    }
  }

  async function onCancelScan() {
    if (!activeScan) {
      return;
    }
    await cancelScan(activeScan.id).catch((err) => setError(err instanceof Error ? err.message : 'Unable to cancel scan'));
  }

  const summary = scan?.summary || activeScan;
  const isRunning = summary?.status === 'running';

  return (
    <div className="app-shell">
      <aside className="sidebar">
        <div className="brand">
          <div className="brand-mark">
            <Globe2 size={22} />
          </div>
          <div>
            <strong>EDS Analyser</strong>
            <span>crawler dashboard</span>
          </div>
        </div>

        <form className="scan-form" onSubmit={onStartScan}>
          <label htmlFor="site-url">EDS URL</label>
          <div className="input-row">
            <input
              id="site-url"
              value={url}
              onChange={(event) => setUrl(event.target.value)}
              placeholder="https://example.com"
              disabled={loading || isRunning}
            />
            <button type="submit" className="icon-button primary" disabled={loading || isRunning || !url.trim()} title="Start scan">
              {loading ? <Loader2 className="spin" size={18} /> : <Play size={18} />}
            </button>
          </div>
          {isRunning && (
            <button type="button" className="ghost action-row" onClick={onCancelScan}>
              <StopCircle size={17} />
              Cancel scan
            </button>
          )}
        </form>

        <nav className="tabs">
          {tabs.map((item) => {
            const Icon = item.icon;
            return (
              <button key={item.id} type="button" className={tab === item.id ? 'active' : ''} onClick={() => setTab(item.id)}>
                <Icon size={17} />
                {item.label}
              </button>
            );
          })}
        </nav>
      </aside>

      <main className="workspace">
        <header className="topbar">
          <div>
            <p className="eyebrow">Site health</p>
            <h1>{summary ? readableHost(summary.rootUrl) : 'Ready to crawl'}</h1>
          </div>
          <div className={`status-pill ${summary?.status || 'idle'}`}>
            {isRunning && <Loader2 className="spin" size={16} />}
            {summary?.status || 'idle'}
          </div>
        </header>

        {error && (
          <div className="alert">
            <OctagonX size={18} />
            {error}
          </div>
        )}

        {summary && (
          <section className="progress-band">
            <Metric label="Health" value={formatScore(summary.scores.health)} tone={scoreTone(summary.scores.health)} />
            <Metric label="Pages" value={`${summary.completedPages}/${summary.discoveredPages}`} />
            <Metric label="Failures" value={summary.failedPages.toString()} tone={summary.failedPages ? 'warn' : 'good'} />
            <Metric label="Performance" value={formatScore(summary.scores.performance)} tone={scoreTone(summary.scores.performance)} />
            <Metric label="SEO" value={formatScore(summary.scores.seo)} tone={scoreTone(summary.scores.seo)} />
          </section>
        )}

        {events.length > 0 && (
          <section className="event-strip">
            {events.map((event) => (
              <span key={`${event.timestamp}-${event.type}-${event.pageUrl || ''}`}>
                {event.type.replace('-', ' ')}
                {event.pageUrl ? `: ${compactURL(event.pageUrl)}` : event.message ? `: ${event.message}` : ''}
              </span>
            ))}
          </section>
        )}

        {!scan && <EmptyState history={history} onOpen={(id) => void loadScan(id)} />}

        {scan && tab === 'overview' && <Overview scan={scan} />}
        {scan && tab === 'pages' && (
          <PagesView
            pages={filteredPages}
            selectedPage={selectedPage}
            pageFilter={pageFilter}
            onFilter={setPageFilter}
            onSelect={setSelectedPageURL}
          />
        )}
        {scan && tab === 'blocks' && <BlocksView blocks={scan.blocks} sections={scan.sections} />}
        {scan && tab === 'links' && <LinksView scan={scan} />}
        {scan && tab === 'seo' && <SEOView scan={scan} />}
        {tab === 'history' && <HistoryView history={history} currentID={summary?.id} onOpen={(id) => void loadScan(id)} />}
      </main>
    </div>
  );
}

function Overview({ scan }: { scan: ScanResult }) {
  const totals = [
    { label: 'Blocks', value: scan.pages.reduce((sum, page) => sum + page.blockCount, 0) },
    { label: 'Block types', value: scan.blocks.length },
    { label: 'Section variations', value: scan.sections.length },
    { label: 'Links', value: scan.links.total },
    { label: 'Missing titles', value: scan.seo.missingTitle },
    { label: 'Missing OG images', value: scan.seo.missingOgImage },
  ];
  return (
    <section className="content-grid">
      <div className="panel wide">
        <div className="panel-heading">
          <h2>Lighthouse</h2>
          <BarChart3 size={19} />
        </div>
        <div className="score-grid">
          <ScoreGauge label="Performance" score={scan.summary.scores.performance} />
          <ScoreGauge label="Accessibility" score={scan.summary.scores.accessibility} />
          <ScoreGauge label="Best Practices" score={scan.summary.scores.bestPractices} />
          <ScoreGauge label="SEO" score={scan.summary.scores.seo} />
        </div>
      </div>
      <div className="panel">
        <div className="panel-heading">
          <h2>Inventory</h2>
          <Boxes size={19} />
        </div>
        <div className="stat-list">
          {totals.map((item) => (
            <Metric key={item.label} label={item.label} value={item.value.toString()} />
          ))}
        </div>
      </div>
    </section>
  );
}

function PagesView({
  pages,
  selectedPage,
  pageFilter,
  onFilter,
  onSelect,
}: {
  pages: PageResult[];
  selectedPage: PageResult | null;
  pageFilter: string;
  onFilter: (value: string) => void;
  onSelect: (value: string) => void;
}) {
  return (
    <section className="pages-layout">
      <div className="panel table-panel">
        <div className="panel-heading">
          <h2>Pages</h2>
          <div className="search-field">
            <Search size={16} />
            <input value={pageFilter} onChange={(event) => onFilter(event.target.value)} placeholder="Filter pages" />
          </div>
        </div>
        <div className="table-scroll">
          <table>
            <thead>
              <tr>
                <th>URL</th>
                <th>Title</th>
                <th>Health</th>
                <th>Blocks</th>
                <th>Links</th>
              </tr>
            </thead>
            <tbody>
              {pages.map((page) => (
                <tr key={page.url} onClick={() => onSelect(page.url)} className={selectedPage?.url === page.url ? 'selected' : ''}>
                  <td>{compactURL(page.url)}</td>
                  <td>{page.title || 'Missing title'}</td>
                  <td><ScoreBadge score={page.lighthouse.health} /></td>
                  <td>{page.blockCount}</td>
                  <td>{page.linkCount}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>
      <PageDetail page={selectedPage} />
    </section>
  );
}

function PageDetail({ page }: { page: PageResult | null }) {
  if (!page) {
    return <div className="panel detail-panel"><h2>Page detail</h2></div>;
  }
  return (
    <div className="panel detail-panel">
      <div className="panel-heading">
        <h2>Page detail</h2>
        <a href={page.url} target="_blank" rel="noreferrer" className="icon-link" title="Open page">
          <ExternalLink size={17} />
        </a>
      </div>
      <dl className="detail-list">
        <dt>Title</dt><dd>{page.title || 'Missing'}</dd>
        <dt>H1</dt><dd>{page.h1 || 'Missing'}</dd>
        <dt>Status</dt><dd>{page.statusCode || 'n/a'}</dd>
        <dt>Canonical</dt><dd>{page.canonical || 'Missing'}</dd>
        <dt>Description</dt><dd>{page.description || 'Missing'}</dd>
        <dt>OG title</dt><dd>{page.og.title || 'Missing'}</dd>
        <dt>OG image</dt><dd>{page.og.image || 'Missing'}</dd>
      </dl>
      <div className="mini-grid">
        <Metric label="Sections" value={page.sectionCount.toString()} />
        <Metric label="Blocks" value={page.blockCount.toString()} />
        <Metric label="Internal" value={page.internalLinks.toString()} />
        <Metric label="External" value={page.externalLinks.toString()} />
      </div>
      {(page.fetchError || page.auditError) && (
        <div className="warning-box">
          {page.fetchError || page.auditError}
        </div>
      )}
      <h3>Blocks</h3>
      <div className="chip-row">
        {page.blocks.map((block, index) => (
          <span key={`${block.name}-${index}`} className="chip">{block.name}{block.variations.length ? ` / ${block.variations.join(', ')}` : ''}</span>
        ))}
      </div>
      <h3>Links</h3>
      <div className="link-list">
        {page.links.slice(0, 20).map((link, index) => (
          <a key={`${link.url}-${index}`} href={link.url} target="_blank" rel="noreferrer">
            <span>{link.kind}</span>
            {link.text || compactURL(link.url)}
          </a>
        ))}
      </div>
    </div>
  );
}

function BlocksView({ blocks, sections }: { blocks: BlockStat[]; sections: SectionStat[] }) {
  return (
    <section className="content-grid">
      <StatTable
        title="Blocks"
        rows={blocks.map((block) => ({
          name: block.name,
          count: block.count,
          detail: Object.entries(block.variations).map(([name, count]) => `${name} ${count}`).join(', ') || 'base',
        }))}
      />
      <StatTable
        title="Section variations"
        rows={sections.map((section) => ({
          name: section.variation,
          count: section.count,
          detail: `${section.pages.length} pages`,
        }))}
      />
    </section>
  );
}

function LinksView({ scan }: { scan: ScanResult }) {
  const allLinks = scan.pages.flatMap((page) => page.links);
  return (
    <section className="content-grid">
      <div className="panel">
        <div className="panel-heading"><h2>Links</h2><Link2 size={19} /></div>
        <div className="stat-list">
          <Metric label="Total" value={scan.links.total.toString()} />
          <Metric label="Internal" value={scan.links.internal.toString()} />
          <Metric label="External" value={scan.links.external.toString()} />
          <Metric label="Assets" value={scan.links.asset.toString()} />
          <Metric label="Unique internal" value={scan.links.uniqueInternal.toString()} />
          <Metric label="Unique external" value={scan.links.uniqueExternal.toString()} />
        </div>
      </div>
      <div className="panel wide table-panel">
        <div className="panel-heading"><h2>All links</h2></div>
        <div className="table-scroll">
          <table>
            <thead><tr><th>Kind</th><th>Text</th><th>URL</th><th>Page</th></tr></thead>
            <tbody>
              {allLinks.slice(0, 250).map((link, index) => (
                <tr key={`${link.url}-${index}`}>
                  <td>{link.kind}</td>
                  <td>{link.text || '-'}</td>
                  <td>{compactURL(link.url)}</td>
                  <td>{link.pageUrl ? compactURL(link.pageUrl) : '-'}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>
    </section>
  );
}

function SEOView({ scan }: { scan: ScanResult }) {
  return (
    <section className="content-grid">
      <div className="panel">
        <div className="panel-heading"><h2>SEO gaps</h2><ShieldCheck size={19} /></div>
        <div className="stat-list">
          <Metric label="Missing title" value={scan.seo.missingTitle.toString()} tone={scan.seo.missingTitle ? 'warn' : 'good'} />
          <Metric label="Missing description" value={scan.seo.missingDescription.toString()} tone={scan.seo.missingDescription ? 'warn' : 'good'} />
          <Metric label="Missing H1" value={scan.seo.missingH1.toString()} tone={scan.seo.missingH1 ? 'warn' : 'good'} />
          <Metric label="Missing canonical" value={scan.seo.missingCanonical.toString()} tone={scan.seo.missingCanonical ? 'warn' : 'good'} />
          <Metric label="Missing OG title" value={scan.seo.missingOgTitle.toString()} tone={scan.seo.missingOgTitle ? 'warn' : 'good'} />
          <Metric label="Missing OG image" value={scan.seo.missingOgImage.toString()} tone={scan.seo.missingOgImage ? 'warn' : 'good'} />
        </div>
      </div>
      <div className="panel wide table-panel">
        <div className="panel-heading"><h2>Open Graph</h2></div>
        <div className="table-scroll">
          <table>
            <thead><tr><th>Page</th><th>OG title</th><th>OG image</th><th>OG URL</th></tr></thead>
            <tbody>
              {scan.pages.map((page) => (
                <tr key={page.url}>
                  <td>{compactURL(page.url)}</td>
                  <td>{page.og.title || 'Missing'}</td>
                  <td>{page.og.image ? compactURL(page.og.image) : 'Missing'}</td>
                  <td>{page.og.url ? compactURL(page.og.url) : 'Missing'}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>
    </section>
  );
}

function HistoryView({ history, currentID, onOpen }: { history: ScanSummary[]; currentID?: string; onOpen: (id: string) => void }) {
  return (
    <section className="panel table-panel">
      <div className="panel-heading"><h2>History</h2><History size={19} /></div>
      <div className="table-scroll">
        <table>
          <thead><tr><th>Site</th><th>Status</th><th>Pages</th><th>Health</th><th>Started</th></tr></thead>
          <tbody>
            {history.map((item) => (
              <tr key={item.id} onClick={() => onOpen(item.id)} className={item.id === currentID ? 'selected' : ''}>
                <td>{readableHost(item.rootUrl)}</td>
                <td>{item.status}</td>
                <td>{item.completedPages}/{item.discoveredPages}</td>
                <td><ScoreBadge score={item.scores.health} /></td>
                <td>{new Date(item.startedAt).toLocaleString()}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  );
}

function EmptyState({ history, onOpen }: { history: ScanSummary[]; onOpen: (id: string) => void }) {
  return (
    <section className="empty-state">
      <div>
        <Activity size={24} />
        <h2>No scan selected</h2>
      </div>
      {history.slice(0, 4).map((item) => (
        <button key={item.id} type="button" onClick={() => onOpen(item.id)}>
          <span>{readableHost(item.rootUrl)}</span>
          <ScoreBadge score={item.scores.health} />
        </button>
      ))}
    </section>
  );
}

function StatTable({ title, rows }: { title: string; rows: Array<{ name: string; count: number; detail: string }> }) {
  return (
    <div className="panel table-panel">
      <div className="panel-heading"><h2>{title}</h2></div>
      <div className="table-scroll">
        <table>
          <thead><tr><th>Name</th><th>Count</th><th>Detail</th></tr></thead>
          <tbody>
            {rows.map((row) => (
              <tr key={row.name}>
                <td>{row.name}</td>
                <td>{row.count}</td>
                <td>{row.detail}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function Metric({ label, value, tone }: { label: string; value: string; tone?: 'good' | 'warn' | 'bad' | 'muted' }) {
  return (
    <div className={`metric ${tone || ''}`}>
      <span>{label}</span>
      <strong>{value}</strong>
    </div>
  );
}

function ScoreGauge({ label, score }: { label: string; score: number | null }) {
  const width = Math.max(0, Math.min(100, score || 0));
  return (
    <div className="score-gauge">
      <div className="score-label">
        <span>{label}</span>
        <strong>{formatScore(score)}</strong>
      </div>
      <div className="bar"><span style={{ width: `${width}%` }} /></div>
    </div>
  );
}

function ScoreBadge({ score }: { score: number | null }) {
  return <span className={`score-badge ${scoreTone(score)}`}>{formatScore(score)}</span>;
}

function formatScore(score: number | null | undefined) {
  return typeof score === 'number' ? Math.round(score).toString() : '-';
}

function scoreTone(score: number | null | undefined): 'good' | 'warn' | 'bad' | 'muted' {
  if (typeof score !== 'number') {
    return 'muted';
  }
  if (score >= 90) {
    return 'good';
  }
  if (score >= 50) {
    return 'warn';
  }
  return 'bad';
}

function readableHost(raw: string) {
  try {
    return new URL(raw).host;
  } catch {
    return raw;
  }
}

function compactURL(raw: string) {
  try {
    const parsed = new URL(raw);
    return `${parsed.host}${parsed.pathname === '/' ? '' : parsed.pathname}`;
  } catch {
    return raw;
  }
}

const emptyLinks = { total: 0, internal: 0, external: 0, asset: 0, mail: 0, tel: 0, hash: 0, uniqueInternal: 0, uniqueExternal: 0 };
const emptySEO = { missingTitle: 0, missingDescription: 0, missingH1: 0, missingCanonical: 0, missingOgTitle: 0, missingOgImage: 0, missingOgUrl: 0 };
