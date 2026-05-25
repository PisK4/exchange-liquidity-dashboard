export type SearchParams = Record<string, string | string[] | undefined>;

export function dashboardRedirect(tab: string, searchParams: SearchParams, defaults: Record<string, string> = {}) {
  const params = new URLSearchParams();
  for (const [key, value] of Object.entries(searchParams)) {
    const scalar = Array.isArray(value) ? value[0] : value;
    if (scalar) params.set(key, scalar);
  }
  params.set('tab', tab);
  for (const [key, value] of Object.entries(defaults)) {
    if (!params.has(key)) params.set(key, value);
  }
  return `/?${params.toString()}`;
}
