import { getJSON, getJSONWithFallback } from './fetcher';

export const activityDecisionActions = ['follow_now', 'benchmark_watch', 'differentiate', 'no_follow', 'ignore_duplicate'] as const;

export type ActivityDecisionAction = (typeof activityDecisionActions)[number];

export type ActivityEventSummary = {
  id: number;
  raw_evidence_id?: number;
  platform: string;
  source_group: string;
  source_url?: string;
  title: string;
  activity_type: string;
  content_text?: string;
  reward_pool_text?: string;
  start_time?: string | null;
  end_time?: string | null;
  raw_time_text?: string;
  review_status: string;
  ops_decision_action?: string;
  event_status?: string;
  event_version: number;
  content_hash: string;
  dedupe_key: string;
  publish_time?: string | null;
  needs_human_review?: boolean;
  auto_push_allowed?: boolean;
  parser_warnings?: string[];
  rich_fields_summary?: Record<string, unknown>;
};

export type ActivityEventSymbol = {
  canonicalSymbol?: string;
  canonical_symbol?: string;
  displaySymbol?: string;
  display_symbol?: string;
  marketSurface?: string;
  market_surface?: string;
  role?: string;
};

export type ActivityRawEvidenceRef = {
  id?: number;
  source_key?: string;
  platform?: string;
  source_group?: string;
  source_url?: string;
  fetch_mode?: string;
  fetched_at?: string;
  payload_hash?: string;
  payload_preview?: string;
  payload_size_bytes?: number;
  payload_truncated?: boolean;
  schema_hash?: string;
  content_hash?: string;
};

export type ActivityEventsResponse = {
  items: ActivityEventSummary[];
  next_cursor?: string;
};

export type ActivityEventDetailResponse = {
  event: ActivityEventSummary;
  symbols?: ActivityEventSymbol[];
  raw_evidence_refs?: ActivityRawEvidenceRef[];
};

export type ActivitySourceHealth = {
  id?: number;
  platform: string;
  sourceGroup?: string;
  source_group?: string;
  sourceType?: string;
  source_type?: string;
  sourceURL?: string;
  source_url?: string;
  fetchMode?: string;
  fetch_mode?: string;
  enabled: boolean;
  autoPushEnabled?: boolean;
  auto_push_enabled?: boolean;
  sourceStatus?: string;
  source_status?: string;
  lastHTTPStatus?: number | null;
  last_http_status?: number | null;
  lastErrorKind?: string;
  last_error_kind?: string;
  updatedAt?: string;
  updated_at?: string;
};

export type ActivitySourceHealthResponse = {
  items: ActivitySourceHealth[];
};

export type ActivityDelivery = {
  id: number;
  event_type: string;
  dedupe_key: string;
  target_channel: string;
  status: string;
  attempt_count: number;
  max_attempts: number;
  last_error?: string;
  created_at?: string;
  updated_at?: string;
};

export type ActivityDeliveriesResponse = {
  items: ActivityDelivery[];
  next_cursor?: string;
};

export type ActivityEventFilter = {
  platform?: string;
  activity_type?: string;
  status?: string;
  review_status?: string;
  limit?: number;
};

export function activityEventsPath(filter: ActivityEventFilter = {}): string {
  const params = new URLSearchParams();
  for (const [key, value] of Object.entries(filter)) {
    if (value !== undefined && value !== '') params.set(key, String(value));
  }
  const qs = params.toString();
  return qs ? `/api/activity/events?${qs}` : '/api/activity/events';
}

export function normalizeActivityDecisionAction(raw: string | null | undefined): ActivityDecisionAction {
  const value = raw ?? '';
  return activityDecisionActions.includes(value as ActivityDecisionAction) ? value as ActivityDecisionAction : 'benchmark_watch';
}

export function getActivityEvents(filter: ActivityEventFilter = {}) {
  return getJSONWithFallback<ActivityEventsResponse>(activityEventsPath(filter));
}

export function getActivityEventDetail(id: string | number) {
  return getJSONWithFallback<ActivityEventDetailResponse>(`/api/activity/events/${id}`);
}

export function getActivitySourceHealth() {
  return getJSONWithFallback<ActivitySourceHealthResponse>('/api/activity/source-health');
}

export function getActivityDeliveries() {
  return getJSONWithFallback<ActivityDeliveriesResponse>('/api/activity/deliveries?limit=20');
}

export function postActivityDecision(id: string | number, body: { action: ActivityDecisionAction; version: number; token: string; reviewer?: string; reason?: string }) {
  return getJSON<{ status: string; event_id: number; action: string }>(`/api/activity/decision/${id}`, {
    method: 'POST',
    body: JSON.stringify(body),
  });
}
