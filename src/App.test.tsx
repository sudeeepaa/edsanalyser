import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import App from './App';

const fetchMock = vi.fn();

beforeEach(() => {
  fetchMock.mockReset();
  globalThis.fetch = fetchMock;
  class FakeEventSource {
    url: string;
    onerror: (() => void) | null = null;
    constructor(url: string) {
      this.url = url;
    }
    addEventListener() {}
    removeEventListener() {}
    close() {}
  }
  // @ts-expect-error test shim
  globalThis.EventSource = FakeEventSource;
});

describe('App', () => {
  it('renders the dashboard shell and loads history', async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse([]));
    render(<App />);
    expect(screen.getByText('EDS Analyser')).toBeInTheDocument();
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith('/api/scans', expect.any(Object)));
  });

  it('handles empty API history encoded as null', async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(null));
    render(<App />);
    expect(await screen.findByText('No scan selected')).toBeInTheDocument();
  });

  it('starts a scan from the URL form', async () => {
    fetchMock
      .mockResolvedValueOnce(jsonResponse([]))
      .mockResolvedValueOnce(jsonResponse({
        id: 'scan-1',
        inputUrl: 'https://example.com',
        rootUrl: 'https://example.com/',
        status: 'running',
        startedAt: new Date().toISOString(),
        discoveredPages: 0,
        completedPages: 0,
        failedPages: 0,
        scores: { performance: null, accessibility: null, bestPractices: null, seo: null, health: null },
      }))
      .mockResolvedValueOnce(jsonResponse([]));

    render(<App />);
    await userEvent.type(screen.getByLabelText('EDS URL'), 'https://example.com');
    await userEvent.click(screen.getByTitle('Start scan'));

    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith('/api/scans', expect.objectContaining({ method: 'POST' })));
    expect(await screen.findByText('example.com')).toBeInTheDocument();
  });
});

function jsonResponse(payload: unknown) {
  return {
    ok: true,
    json: async () => payload,
  } as Response;
}
