import { getJSONWithFallback } from './fetcher';
import type {
  ListingCandidateDetailResponse,
  ListingCandidateListResponse,
  ListingDeliveryListResponse,
  ListingSignalListResponse,
  ListingSourceHealthResponse,
} from './types';

export type ListingCandidateFilter = {
  limit?: number;
  status?: string;
  evidence_kind?: string;
  platform?: string;
  symbol?: string;
};

export type ListingDeliveryFilter = {
  limit?: number;
  event_type?: string;
  status?: string;
};

function pathWithQuery(base: string, filter: Record<string, string | number | undefined> = {}): string {
  const params = new URLSearchParams();
  for (const [key, value] of Object.entries(filter)) {
    if (value !== undefined && value !== '') params.set(key, String(value));
  }
  const qs = params.toString();
  return qs ? `${base}?${qs}` : base;
}

export function listingCandidatesPath(filter: ListingCandidateFilter = {}): string {
  return pathWithQuery('/api/listing/candidates', filter);
}

export function listingDeliveriesPath(filter: ListingDeliveryFilter = {}): string {
  return pathWithQuery('/api/listing/deliveries', filter);
}

export function getListingCandidates(filter: ListingCandidateFilter = {}) {
  return getJSONWithFallback<ListingCandidateListResponse>(listingCandidatesPath(filter));
}

export function getListingCandidateDetail(id: string | number) {
  return getJSONWithFallback<ListingCandidateDetailResponse>(`/api/listing/candidates/${id}`);
}

export function getListingCandidateSignals(id: string | number) {
  return getJSONWithFallback<ListingSignalListResponse>(`/api/listing/candidates/${id}/signals`);
}

export function getListingSourceHealth() {
  return getJSONWithFallback<ListingSourceHealthResponse>('/api/listing/source-health');
}

export function getListingDeliveries(filter: ListingDeliveryFilter = { limit: 20 }) {
  return getJSONWithFallback<ListingDeliveryListResponse>(listingDeliveriesPath(filter));
}
