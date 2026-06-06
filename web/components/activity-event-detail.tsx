'use client';

import Link from 'next/link';
import { useEffect, useState } from 'react';
import { ActivityTopbar } from '@/components/activity-radar';
import { StatusEmptyState } from '@/components/status-empty-state';
import { getActivityEventDetail, type ActivityEventDetailResponse } from '@/lib/api/activity';

function displayValue(value: string | null | undefined) {
  return value && value.trim() ? value : '—';
}

function formatDateTime(value: string | null | undefined) {
  if (!value) return '—';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString('zh-CN', { hour12: false });
}

function formatBytes(value: number | undefined) {
  if (!value || value <= 0) return '—';
  if (value < 1024) return `${value} B`;
  if (value < 1024 * 1024) return `${(value / 1024).toFixed(1)} KB`;
  return `${(value / 1024 / 1024).toFixed(1)} MB`;
}

export function ActivityEventDetail({ id }: { id: string }) {
  const [data, setData] = useState<ActivityEventDetailResponse | null>(null);
  const [error, setError] = useState('');

  useEffect(() => {
    let alive = true;
    setError('');
    getActivityEventDetail(id).then(res => {
      if (alive) setData(res);
    }).catch(err => {
      if (alive) setError(err instanceof Error ? err.message : String(err));
    });
    return () => { alive = false; };
  }, [id]);

  return (
    <>
      <ActivityTopbar />
      <main className="dashboard-main">
        {error ? <StatusEmptyState status="error" message={`Activity detail unavailable: ${error}`} /> : null}
        {!data && !error ? <StatusEmptyState status="partial" message="加载 Activity 详情..." /> : null}
        {data ? (
          <div className="grid">
            <section className="panel span-24">
              <div className="panel-head">
                <span className="panel-title">{data.event.title}</span>
                <span className="panel-tag">v{data.event.event_version}</span>
              </div>
              <div className="activity-detail-body">
                <div><strong>平台</strong><br />{data.event.platform}</div>
                <div><strong>Source</strong><br />{data.event.source_group}</div>
                <div><strong>类型</strong><br />{data.event.activity_type || '—'}</div>
                <div><strong>发布时间</strong><br />{formatDateTime(data.event.publish_time)}</div>
                <div><strong>开始时间</strong><br />{formatDateTime(data.event.start_time)}</div>
                <div><strong>结束时间</strong><br />{formatDateTime(data.event.end_time)}</div>
                <div><strong>复核</strong><br />{data.event.review_status}</div>
                <div><strong>运营决策</strong><br />{data.event.ops_decision_action || '—'}</div>
                <div><strong>原始时间</strong><br />{displayValue(data.event.raw_time_text)}</div>
              </div>
              <div className="activity-content-block">
                <strong>内容</strong>
                <p>{displayValue(data.event.content_text)}</p>
              </div>
              {data.event.reward_pool_text ? (
                <div className="activity-content-block compact">
                  <strong>奖池 / 激励</strong>
                  <p>{data.event.reward_pool_text}</p>
                </div>
              ) : null}
              {data.event.parser_warnings?.length ? (
                <div className="activity-content-block compact">
                  <strong>解析提示</strong>
                  <ul>{data.event.parser_warnings.map(warning => <li key={warning}>{warning}</li>)}</ul>
                </div>
              ) : null}
              <div className="panel-foot-note">
                {data.event.source_url ? <a href={data.event.source_url} target="_blank" rel="noreferrer">打开原始来源</a> : null}
                <span> · </span>
                <Link href="/activity">返回 Activity Radar</Link>
              </div>
            </section>
            <section className="panel span-12">
              <div className="panel-head"><span className="panel-title">Symbols</span></div>
              <pre className="json-block">{JSON.stringify(data.symbols ?? [], null, 2)}</pre>
            </section>
            <section className="panel span-12">
              <div className="panel-head"><span className="panel-title">Raw Evidence</span></div>
              <div className="activity-evidence-list">
                {(data.raw_evidence_refs ?? []).length ? data.raw_evidence_refs?.map(raw => (
                  <article className="activity-evidence-card" key={raw.id ?? raw.payload_hash ?? raw.source_key}>
                    <div><strong>{raw.platform ?? data.event.platform}</strong> · {raw.source_group ?? data.event.source_group} · {raw.fetch_mode ?? '—'}</div>
                    <div className="muted">{formatDateTime(raw.fetched_at)} · {formatBytes(raw.payload_size_bytes)}{raw.payload_truncated ? ' · truncated' : ''}</div>
                    {raw.source_url ? <a href={raw.source_url} target="_blank" rel="noreferrer">打开采集源</a> : null}
                    <pre className="json-block evidence-preview">{raw.payload_preview || raw.payload_hash || '—'}</pre>
                  </article>
                )) : <div className="empty-state">暂无 raw evidence preview</div>}
              </div>
            </section>
            <section className="panel span-24">
              <div className="panel-head"><span className="panel-title">Debug Metadata</span></div>
              <pre className="json-block">{JSON.stringify({
                dedupe_key: data.event.dedupe_key,
                raw_evidence_id: data.event.raw_evidence_id,
                rich_fields_summary: data.event.rich_fields_summary ?? {},
              }, null, 2)}</pre>
            </section>
          </div>
        ) : null}
      </main>
    </>
  );
}
