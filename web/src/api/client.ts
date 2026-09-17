export type APIError = {
  code: string
  message: string
  request_id: string
}

export class APIRequestError extends Error {
  readonly detail: APIError

  constructor(detail: APIError) {
    super(detail.message)
    this.name = 'APIRequestError'
    this.detail = detail
  }
}

let csrfToken: string | null = null

export function setCSRFToken(token: string | null): void {
  csrfToken = token
}

export function readCSRFCookie(): string | null {
  const prefix = 'controlpanel_csrf='
  const cookie = document.cookie.split('; ').find((entry) => entry.startsWith(prefix))
  return cookie ? decodeURIComponent(cookie.slice(prefix.length)) : null
}

export async function apiRequest<T>(path: string, init: RequestInit = {}): Promise<T> {
  const method = (init.method ?? 'GET').toUpperCase()
  const headers = new Headers(init.headers)
  if (init.body && !headers.has('Content-Type')) {
    headers.set('Content-Type', 'application/json')
  }
  if (!['GET', 'HEAD', 'OPTIONS'].includes(method) && csrfToken) {
    headers.set('X-CSRF-Token', csrfToken)
  }

  const response = await fetch(`/api/v1${path}`, {
    ...init,
    method,
    headers,
    credentials: 'include',
  })
  if (!response.ok) {
    const payload = await response.json().catch(() => null) as { error?: APIError } | null
    const detail = payload?.error ?? {
      code: 'unexpected_response',
      message: 'The server returned an unexpected response',
      request_id: '',
    }
    if (response.status === 401) setCSRFToken(null)
    throw new APIRequestError(detail)
  }
  if (response.status === 204) return undefined as T
  return await response.json() as T
}
