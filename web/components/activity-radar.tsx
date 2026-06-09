'use client';

import Link from 'next/link';
import { useEffect, useMemo, useState } from 'react';
import { StatusEmptyState } from '@/components/status-empty-state';
import { getActivityDeliveries, getActivityEvents, getActivitySourceHealth, type ActivityDelivery, type ActivityEventFilter, type ActivityEventSummary, type ActivitySourceHealth } from '@/lib/api/activity';

type ActivityRadarProps = {
  query: ActivityEventFilter;
};

type ActivityRadarData = {
  events: ActivityEventSummary[];
  sourceHealth: ActivitySourceHealth[];
  deliveries: ActivityDelivery[];
};

const platforms = ['binance', 'okx', 'bingx', 'gate', 'mexc', 'bybit', 'bitget', 'hyperliquid', 'lighter'];
const reviewStatuses = ['pending', 'approved', 'rejected'];

export function ActivityRadar({ query }: ActivityRadarProps) {
  const [data, setData] = useState<ActivityRadarData | null>(null);
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(true);
  const requestFilter = useMemo(() => ({
    platform: query.platform,
    review_status: query.review_status,
    status: query.status,
    activity_type: query.activity_type,
    limit: 50,
  }), [query.platform, query.review_status, query.status, query.activity_type]);

  useEffect(() => {
    let alive = true;
    setLoading(true);
    setError('');
    Promise.all([
      getActivityEvents(requestFilter),
      getActivitySourceHealth(),
      getActivityDeliveries(),
    ]).then(([events, health, deliveries]) => {
      if (alive) setData({ events: events.items ?? [], sourceHealth: health.items ?? [], deliveries: deliveries.items ?? [] });
    }).catch(err => {
      if (err instanceof DOMException && err.name === 'AbortError') return;
      if (alive) setError(err instanceof Error ? err.message : String(err));
    }).finally(() => {
      if (alive) setLoading(false);
    });
    return () => { alive = false; };
  }, [requestFilter]);

  const counts = useMemo(() => {
    const events = data?.events ?? [];
    return {
      total: events.length,
      review: events.filter(ev => ev.needs_human_review || ev.review_status === 'pending').length,
      auto: events.filter(ev => ev.auto_push_allowed).length,
      sent: data?.deliveries.filter(row => row.status === 'sent').length ?? 0,
    };
  }, [data]);

  return (
    <>
      <ActivityTopbar />
      <main className="dashboard-main">
        <section className="global-controls">
          <span className="control-label">
            <span>平台</span>
            <FilterPills base="/activity" param="platform" items={platforms} active={query.platform} query={query} />
          </span>
          <span className="control-label">
            <span>复核</span>
            <FilterPills base="/activity" param="review_status" items={reviewStatuses} active={query.review_status} query={query} />
          </span>
        </section>
        {error ? <StatusEmptyState status="error" message={`Activity API unavailable: ${error}`} /> : null}
        {loading && !data ? <StatusEmptyState status="partial" message="加载 Activity Radar..." /> : null}
        {data ? (
          <div className="grid">
            <section className="panel span-24">
              <div className="panel-head">
                <span className="panel-title">Activity Radar</span>
                <span className="panel-sub">竞品运营活动情报 · 不展示 Top30 上下文</span>
                <span className="panel-tag">events {counts.total}</span>
              </div>
              <div className="kpi-row">
                <KPI label="待复核" value={counts.review} />
                <KPI label="可自动通知" value={counts.auto} />
                <KPI label="已发送" value={counts.sent} />
              </div>
              <ActivityEventTable events={data.events} />
            </section>
            <section className="panel span-12">
              <div className="panel-head">
                <span className="panel-title">Source Health</span>
                <span className="panel-tag">{data.sourceHealth.length} sources</span>
              </div>
              <ActivitySourceHealthTable rows={data.sourceHealth} />
            </section>
            <section className="panel span-12">
              <div className="panel-head">
                <span className="panel-title">Delivery Outbox</span>
                <span className="panel-tag">{data.deliveries.length} rows</span>
              </div>
              <ActivityDeliveryTable rows={data.deliveries} />
            </section>
          </div>
        ) : null}
      </main>
    </>
  );
}

export function ActivityTopbar() {
  return (
    <>
      <header className="topbar">
        <Link className="logo" href="/">EdgeX Ops Intelligence</Link>
        <span className="muted">Operations Activity Agent</span>
        <span className="spacer" />
        <Link className="pill" href="/">Liquidity</Link>
      </header>
      <nav className="tabs">
        <Link className="tab active" href="/activity">Activity Radar</Link>
        <Link className="tab" href="/activity?review_status=pending">待复核</Link>
        <Link className="tab" href="/activity?status=active">Active</Link>
      </nav>
    </>
  );
}

function FilterPills({ base, param, items, active, query }: { base: string; param: keyof ActivityEventFilter; items: string[]; active?: string; query: ActivityEventFilter }) {
  return (
    <span className="pill-group">
      <Link className={`pill ${!active ? 'active' : ''}`} href={href(base, query, { [param]: undefined })}>all</Link>
      {items.map(item => (
        <Link className={`pill ${item === active ? 'active' : ''}`} href={href(base, query, { [param]: item })} key={item}>
          {item}
        </Link>
      ))}
    </span>
  );
}

function href(base: string, query: ActivityEventFilter, patch: Partial<ActivityEventFilter>) {
  const params = new URLSearchParams();
  for (const [key, value] of Object.entries({ ...query, ...patch })) {
    if (value) params.set(key, String(value));
  }
  const qs = params.toString();
  return qs ? `${base}?${qs}` : base;
}

function KPI({ label, value }: { label: string; value: number }) {
  return (
    <div className="kpi-card">
      <div className="kpi-label">{label}</div>
      <div className="kpi-value">{value}</div>
    </div>
  );
}

function ActivityEventTable({ events }: { events: ActivityEventSummary[] }) {
  if (events.length === 0) return <StatusEmptyState status="partial" message="暂无 Activity 事件" />;
  return (
    <div className="table-wrap">
      <table className="tbl">
        <thead>
          <tr>
            <th>平台</th>
            <th>标题</th>
            <th>类型</th>
            <th>复核</th>
            <th>决策</th>
            <th>版本</th>
          </tr>
        </thead>
        <tbody>
          {events.map(ev => (
            <tr key={ev.id}>
              <td>{ev.platform}</td>
              <td><Link href={`/activity/events/${ev.id}`}>{ev.title}</Link></td>
              <td>{ev.activity_type || '—'}</td>
              <td><StatusPill value={ev.review_status} /></td>
              <td>{ev.ops_decision_action || '—'}</td>
              <td>{ev.event_version}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function ActivitySourceHealthTable({ rows }: { rows: ActivitySourceHealth[] }) {
  if (rows.length === 0) return <StatusEmptyState status="partial" message="暂无 source health 数据" />;
  return (
    <div className="table-wrap">
      <table className="tbl">
        <thead><tr><th>平台</th><th>Source</th><th>状态</th><th>最近检查</th><th>最近成功</th><th>样本/事件</th><th>Error</th></tr></thead>
        <tbody>
          {rows.map((row, idx) => (
            <tr key={`${row.platform}-${sourceGroup(row)}-${idx}`}>
              <td>{row.platform}</td>
              <td>{sourceGroup(row)}</td>
              <td><StatusPill value={sourceStatus(row)} /></td>
              <td>{formatShortTime(row.last_checked_at ?? row.lastCheckedAt)}</td>
              <td>{formatShortTime(row.last_success_at ?? row.lastSuccessAt)}</td>
              <td>{sourceCounts(row)}</td>
              <td>{sourceError(row)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function ActivityDeliveryTable({ rows }: { rows: ActivityDelivery[] }) {
  if (rows.length === 0) return <StatusEmptyState status="partial" message="暂无 delivery outbox 数据" />;
  return (
    <div className="table-wrap">
      <table className="tbl">
        <thead><tr><th>类型</th><th>状态</th><th>尝试</th><th>Dedupe</th></tr></thead>
        <tbody>
          {rows.map(row => (
            <tr key={row.id}>
              <td>{row.event_type}</td>
              <td><StatusPill value={row.status} /></td>
              <td>{row.attempt_count}/{row.max_attempts}</td>
              <td>{row.dedupe_key}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function StatusPill({ value }: { value?: string }) {
  const cls = value === 'ok' || value === 'sent' || value === 'approved' ? 'b-ok' : value === 'pending' || value === 'retry' || value === 'degraded' ? 'b-warn' : value === 'failed' || value === 'blocked' || value === 'rejected' ? 'b-bad' : 'b-mute';
  return <span className={`badge ${cls}`}>{value || '—'}</span>;
}

function sourceGroup(row: ActivitySourceHealth) {
  return row.source_group ?? row.sourceGroup ?? '—';
}

function sourceStatus(row: ActivitySourceHealth) {
  return row.status ?? row.source_status ?? row.sourceStatus ?? '—';
}

function sourceCounts(row: ActivitySourceHealth) {
  const samples = row.sample_count ?? row.sampleCount;
  const events = row.event_count ?? row.eventCount;
  return `${samples ?? '—'}/${events ?? '—'}`;
}

function sourceError(row: ActivitySourceHealth) {
  const http = row.last_http_status ?? row.lastHTTPStatus;
  const error = row.last_error_kind ?? row.lastErrorKind;
  if (http && error) return `${http} / ${error}`;
  return String(http ?? error ?? '—');
}

function formatShortTime(raw?: string | null) {
  if (!raw) return '—';
  const date = new Date(raw);
  if (Number.isNaN(date.getTime())) return raw;
  return date.toLocaleString('zh-CN', {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  });
}
