'use client';

import Link from 'next/link';
import { useEffect, useState } from 'react';
import { ActivityTopbar } from '@/components/activity-radar';
import { StatusEmptyState } from '@/components/status-empty-state';
import { getActivityEventDetail, postActivityDecision, type ActivityDecisionAction, type ActivityEventSummary } from '@/lib/api/activity';

const labels: Record<ActivityDecisionAction, string> = {
  follow_now: '立即跟进',
  benchmark_watch: '对标观察',
  differentiate: '差异化设计',
  no_follow: '暂不跟进',
  ignore_duplicate: '忽略/重复',
};

export function ActivityDecisionConfirm({ id, action, version, token }: { id: string; action: ActivityDecisionAction; version: number; token: string }) {
  const [event, setEvent] = useState<ActivityEventSummary | null>(null);
  const [status, setStatus] = useState('');
  const [error, setError] = useState('');
  const [submitting, setSubmitting] = useState(false);

  useEffect(() => {
    let alive = true;
    getActivityEventDetail(id).then(res => {
      if (alive) setEvent(res.event);
    }).catch(err => {
      if (alive) setError(err instanceof Error ? err.message : String(err));
    });
    return () => { alive = false; };
  }, [id]);

  async function submit() {
    setSubmitting(true);
    setError('');
    try {
      const res = await postActivityDecision(id, { action, version, token });
      setStatus(`已提交：${res.action}`);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setSubmitting(false);
    }
  }

  const versionChanged = event ? event.event_version !== version || !token : false;

  return (
    <>
      <ActivityTopbar />
      <main className="dashboard-main">
        <section className="panel span-24">
          <div className="panel-head">
            <span className="panel-title">确认运营决策</span>
            <span className="panel-tag">{labels[action]}</span>
          </div>
          {!event && !error ? <StatusEmptyState status="partial" message="加载活动摘要..." /> : null}
          {event ? (
            <div className="activity-detail-body">
              <div><strong>活动</strong><br />{event.title}</div>
              <div><strong>平台</strong><br />{event.platform}</div>
              <div><strong>当前版本</strong><br />v{event.event_version}</div>
              <div><strong>链接版本</strong><br />v{version || '—'}</div>
              <div><strong>动作</strong><br />{labels[action]}</div>
            </div>
          ) : null}
          {versionChanged ? <StatusEmptyState status="error" message="活动已更新或 token 缺失，请从最新卡片重新判断" /> : null}
          {error ? <StatusEmptyState status="error" message={`提交失败：${error}`} /> : null}
          {status ? <StatusEmptyState status="complete" message={status} /> : null}
          <div className="panel-foot-note">
            <button className="pill active" disabled={submitting || versionChanged || !event} onClick={submit} type="button">
              {submitting ? '提交中...' : '确认提交'}
            </button>
            <span> · </span>
            <Link href={`/activity/events/${id}`}>返回详情</Link>
          </div>
        </section>
      </main>
    </>
  );
}
