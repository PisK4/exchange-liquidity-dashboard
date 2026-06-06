'use client';

import Link from 'next/link';
import { useEffect, useState } from 'react';
import { ActivityTopbar } from '@/components/activity-radar';
import { StatusEmptyState } from '@/components/status-empty-state';
import { getActivityEventDetail, type ActivityEventDetailResponse } from '@/lib/api/activity';

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
                <div><strong>复核</strong><br />{data.event.review_status}</div>
                <div><strong>运营决策</strong><br />{data.event.ops_decision_action || '—'}</div>
                <div><strong>Dedupe</strong><br />{data.event.dedupe_key}</div>
              </div>
              <div className="panel-foot-note">
                {data.event.source_url ? <a href={data.event.source_url} target="_blank" rel="noreferrer">打开原文</a> : null}
                <span> · </span>
                <Link href="/activity">返回 Activity Radar</Link>
              </div>
            </section>
            <section className="panel span-12">
              <div className="panel-head"><span className="panel-title">Symbols</span></div>
              <pre className="json-block">{JSON.stringify(data.symbols ?? [], null, 2)}</pre>
            </section>
            <section className="panel span-12">
              <div className="panel-head"><span className="panel-title">Raw Evidence Refs</span></div>
              <pre className="json-block">{JSON.stringify(data.raw_evidence_refs ?? [], null, 2)}</pre>
            </section>
          </div>
        ) : null}
      </main>
    </>
  );
}
