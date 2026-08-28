// The studio API client — ports the original apiJSON/fetchJSON contract exactly.
// Every /api call carries this session's token (a custom header the studio's
// guardAPI requires and no cross-site page can set). A non-2xx becomes a thrown
// Error carrying the status + plain-text body (the guarded handlers answer
// 400/403/405 with text), so callers surface "403: …" instead of a JSON parse
// error. A 2xx with a non-JSON body degrades to {}.

const meta = document.querySelector('meta[name="keel-token"]') as HTMLMetaElement | null
export const TOKEN = meta?.content ?? ''

export function api(path: string, opts: RequestInit = {}): Promise<Response> {
  const headers: Record<string, string> = {
    ...(opts.headers as Record<string, string> | undefined),
    'X-Keel-Token': TOKEN,
  }
  if (opts.body !== undefined && opts.body !== null) headers['Content-Type'] = 'application/json'
  return fetch(path, { ...opts, headers })
}

export async function apiJSON<T = unknown>(path: string, body?: unknown, method?: string): Promise<T> {
  const r = await api(path, {
    method: method || (body === undefined ? 'GET' : 'POST'),
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  if (!r.ok) {
    const t = await r.text().catch(() => '')
    throw new Error(r.status + (t ? ' ' + t.trim() : ' ' + r.statusText))
  }
  return (await r.json().catch(() => ({}))) as T
}

// fetchJSON is apiJSON that returns {error} instead of throwing — the shape a
// panel uses when it wants to render the error inline rather than try/catch.
export async function fetchJSON<T = unknown>(
  path: string,
  body?: unknown,
  method?: string,
): Promise<T | { error: string }> {
  try {
    return await apiJSON<T>(path, body, method)
  } catch (e) {
    return { error: String((e as Error).message || e) }
  }
}
