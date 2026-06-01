import type { ScanResult, ScanSummary } from './types';

async function request<T>(url: string, init?: RequestInit): Promise<T> {
  const response = await fetch(url, {
    headers: { 'Content-Type': 'application/json' },
    ...init,
  });
  if (!response.ok) {
    const payload = await response.json().catch(() => ({ error: response.statusText }));
    throw new Error(payload.error || response.statusText);
  }
  return response.json() as Promise<T>;
}

export function listScans(): Promise<ScanSummary[]> {
  return request<ScanSummary[] | null>('/api/scans').then((scans) => scans || []);
}

export function startScan(url: string, auditLimit: number | null): Promise<ScanSummary> {
  return request<ScanSummary>('/api/scans', {
    method: 'POST',
    body: JSON.stringify({ url, auditLimit }),
  });
}

export function getScan(id: string): Promise<ScanResult> {
  return request<ScanResult>(`/api/scans/${id}`).then(normalizeScanResult);
}

export function cancelScan(id: string): Promise<{ status: string }> {
  return request<{ status: string }>(`/api/scans/${id}/cancel`, { method: 'POST' });
}

function normalizeScanResult(result: ScanResult): ScanResult {
  return {
    ...result,
    pages: result.pages || [],
    blocks: result.blocks || [],
    sections: result.sections || [],
    links: result.links || { total: 0, internal: 0, external: 0, asset: 0, mail: 0, tel: 0, hash: 0, uniqueInternal: 0, uniqueExternal: 0 },
    seo: result.seo || { missingTitle: 0, missingDescription: 0, missingH1: 0, missingCanonical: 0, missingOgTitle: 0, missingOgImage: 0, missingOgUrl: 0 },
  };
}
