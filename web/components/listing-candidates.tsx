'use client';

import Link from 'next/link';
import { useEffect, useMemo, useState } from 'react';
import { StatusEmptyState } from '@/components/status-empty-state';
import { getListingCandidates, type ListingCandidateFilter } from '@/lib/api/listing';
import type { ListingCandidate } from '@/lib/api/types';

type ListingCandidatesProps = {
  query: ListingCandidateFilter;
};

type ListingCandidatesData = {
  candidates: ListingCandidate[];
  count: number;
};

const lifecycleStatuses = [
  'confirmed_listing_candidate',
  'announced_pending_api_confirmation',
  'api_detected_no_announcement',
  'observed',
  'already_listed',
];

const evidenceKinds = [
  'announcement_and_api',
  'announcement_pending_api',
  'instrument_diff_only',
  'top30_only',
  'manual_seed',
];

const platforms = ['binance', 'okx', 'bybit', 'bitget', 'mexc', 'gate', 'bingx', 'hyperliquid', 'lighter', 'edgeX'];

const labels: Record<string, string> = {
  confirmed_listing_candidate: '已确认候选',
  announced_pending_api_confirmation: '公告待 API 确认',
  api_detected_no_announcement: 'API 已发现',
  observed: '观察中',
  already_listed: '已历史上线',
  announcement_and_api: '公告 + API',
  announcement_pending_api: '公告待确认',
  instrument_diff_only: '仅 API diff',
  top30_only: 'Top30 gap',
  manual_seed: '手工种子',
};

export function ListingCandidates({ query }: ListingCandidatesProps) {
  const [data, setData] = useState<ListingCandidatesData | null>(null);
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(true);
  const requestFilter = useMemo(() => ({
    status: query.status,
    evidence_kind: query.evidence_kind,
    platform: query.platform,
    symbol: query.symbol,
    limit: query.limit ?? 50,
  }), [query.status, query.evidence_kind, query.platform, query.symbol, query.limit]);

  useEffect(() => {
    let alive = true;
    setLoading(true);
    setError('');
    getListingCandidates(requestFilter).then(response => {
      if (alive) setData({ candidates: response.candidates ?? [], count: response.count ?? response.candidates?.length ?? 0 });
    }).catch(err => {
      if (err instanceof DOMException && err.name === 'AbortError') return;
      if (alive) setError(err instanceof Error ? err.message : String(err));
    }).finally(() => {
      if (alive) setLoading(false);
    });
    return () => { alive = false; };
  }, [requestFilter]);

  const summary = useMemo(() => summarizeCandidates(data?.candidates ?? []), [data]);

  return (
    <>
      <ListingTopbar query={query} />
      <main className="dashboard-main">
        <section className="global-controls">
          <span className="control-label">
            <span>状态</span>
            <FilterPills param="status" items={lifecycleStatuses} active={query.status} query={query} />
          </span>
          <span className="control-label">
            <span>证据</span>
            <FilterPills param="evidence_kind" items={evidenceKinds} active={query.evidence_kind} query={query} />
          </span>
          <span className="control-label">
            <span>平台</span>
            <FilterPills param="platform" items={platforms} active={query.platform} query={query} />
          </span>
          {query.symbol ? (
            <span className="control-label">
              <span>Symbol</span>
              <Link className="pill active" href={href({ ...query, symbol: undefined })}>{query.symbol} ×</Link>
            </span>
          ) : null}
        </section>
        {error ? <StatusEmptyState status="error" message={`Listing API unavailable: ${error}`} /> : null}
        {loading && !data ? <StatusEmptyState status="partial" message="加载 Listing candidates..." /> : null}
        {data ? (
          <div className="grid">
            <section className="panel span-24">
              <div className="panel-head">
                <span className="panel-title">Listing Candidates</span>
                <span className="panel-sub">Listing Agent 候选资产 · 手动访问入口</span>
                <span className="panel-tag">{data.count} candidates</span>
              </div>
              <div className="kpi-row">
                <KPI label="已确认候选" value={summary.confirmed} />
                <KPI label="公告待确认" value={summary.announcedPending} />
                <KPI label="API 已发现" value={summary.apiDetected} />
                <KPI label="高置信" value={summary.highConfidence} />
              </div>
              <ListingCandidateTable candidates={data.candidates} />
            </section>
          </div>
        ) : null}
      </main>
    </>
  );
}

function ListingTopbar({ query }: { query: ListingCandidateFilter }) {
  return (
    <>
      <header className="topbar">
        <Link className="logo" href="/">EdgeX Ops Intelligence</Link>
        <span className="muted">Listing Agent</span>
        <span className="spacer" />
        <Link className="pill" href="/">Liquidity</Link>
      </header>
      <nav className="tabs">
        <Link className={`tab ${!query.status && !query.evidence_kind && !query.platform && !query.symbol ? 'active' : ''}`} href="/listing">Candidates</Link>
        <Link className={`tab ${query.status === 'confirmed_listing_candidate' ? 'active' : ''}`} href="/listing?status=confirmed_listing_candidate">已确认候选</Link>
        <Link className={`tab ${query.evidence_kind === 'top30_only' ? 'active' : ''}`} href="/listing?evidence_kind=top30_only">Top30 gap</Link>
      </nav>
    </>
  );
}

function ListingCandidateTable({ candidates }: { candidates: ListingCandidate[] }) {
  if (candidates.length === 0) return <StatusEmptyState status="partial" message="暂无 Listing candidate 数据" />;
  return (
    <div className="table-wrap">
      <table className="tbl">
        <thead>
          <tr>
            <th>ID</th>
            <th>Symbol</th>
            <th>Canonical</th>
            <th>市场</th>
            <th>生命周期</th>
            <th>证据</th>
            <th>置信度</th>
            <th className="num">评分</th>
            <th>建议</th>
            <th>来源</th>
            <th>最近观察</th>
          </tr>
        </thead>
        <tbody>
          {candidates.map((candidate, idx) => (
            <tr key={candidate.id ?? `${candidate.display_symbol}-${candidate.evidence_kind}-${idx}`}>
              <td>{candidate.id ?? '—'}</td>
              <td>{candidate.display_symbol || '—'}</td>
              <td>{candidate.canonical_symbol || '—'}</td>
              <td>{marketLabel(candidate)}</td>
              <td><StatusPill value={candidate.lifecycle_status} label={candidate.lifecycle_status_label} /></td>
              <td>{labelFor(candidate.evidence_kind)}</td>
              <td><StatusPill value={candidate.confidence_level} /></td>
              <td className="num">{scoreLabel(candidate.business_score)}</td>
              <td>{candidate.recommendation_label || labelFor(candidate.recommendation)}</td>
              <td>{sourcePlatforms(candidate)}</td>
              <td>{dateLabel(candidate.last_observed_at)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function FilterPills({ param, items, active, query }: { param: keyof ListingCandidateFilter; items: string[]; active?: string; query: ListingCandidateFilter }) {
  return (
    <span className="pill-group">
      <Link className={`pill ${!active ? 'active' : ''}`} href={href({ ...query, [param]: undefined })}>all</Link>
      {items.map(item => (
        <Link className={`pill ${item === active ? 'active' : ''}`} href={href({ ...query, [param]: item })} key={item}>
          {labelFor(item)}
        </Link>
      ))}
    </span>
  );
}

function href(query: ListingCandidateFilter) {
  const params = new URLSearchParams();
  for (const [key, value] of Object.entries(query)) {
    if (value !== undefined && value !== '') params.set(key, String(value));
  }
  const qs = params.toString();
  return qs ? `/listing?${qs}` : '/listing';
}

function KPI({ label, value }: { label: string; value: number }) {
  return (
    <div className="kpi-card">
      <div className="kpi-label">{label}</div>
      <div className="kpi-value">{value}</div>
    </div>
  );
}

function StatusPill({ value, label }: { value?: string; label?: string }) {
  const cls = value === 'confirmed_listing_candidate' || value === 'high' || value === 'sent'
    ? 'b-ok'
    : value === 'observed' || value === 'medium' || value === 'medium_high' || value === 'pending' || value === 'retry'
      ? 'b-warn'
      : value === 'failed' || value === 'disabled'
        ? 'b-bad'
        : 'b-mute';
  return <span className={`badge ${cls}`}>{label || labelFor(value)}</span>;
}

function summarizeCandidates(candidates: ListingCandidate[]) {
  return {
    confirmed: candidates.filter(row => row.lifecycle_status === 'confirmed_listing_candidate').length,
    announcedPending: candidates.filter(row => row.lifecycle_status === 'announced_pending_api_confirmation').length,
    apiDetected: candidates.filter(row => row.lifecycle_status === 'api_detected_no_announcement').length,
    highConfidence: candidates.filter(row => row.confidence_level === 'high').length,
  };
}

function marketLabel(candidate: ListingCandidate) {
  return [candidate.market_surface, candidate.instrument_kind].filter(Boolean).join(' / ') || '—';
}

function sourcePlatforms(candidate: ListingCandidate) {
  return candidate.source_platforms?.length ? candidate.source_platforms.join(', ') : '—';
}

function scoreLabel(value?: number | null) {
  return typeof value === 'number' && Number.isFinite(value) ? value.toFixed(2) : '—';
}

function dateLabel(value?: string) {
  if (!value) return '—';
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString('zh-CN', { hour12: false });
}

function labelFor(value?: string) {
  if (!value) return '—';
  return labels[value] ?? value;
}
