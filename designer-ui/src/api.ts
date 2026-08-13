import type {
  Address,
  AlertConfig,
  AlertProperties,
  AlertRecord,
  ApiErrorBody,
  Connection,
} from './models'

export class ApiError extends Error {
  readonly status: number
  readonly code?: string
  readonly fields: Record<string, string>

  constructor(status: number, body: ApiErrorBody) {
    super(body.error?.message || `Request failed (${status})`)
    this.name = 'ApiError'
    this.status = status
    this.code = body.error?.code
    this.fields = body.error?.fields ?? {}
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(path, {
    ...init,
    headers: init?.body
      ? { 'Content-Type': 'application/json', ...init.headers }
      : init?.headers,
  })

  if (!response.ok) {
    let body: ApiErrorBody = {}
    try {
      body = (await response.json()) as ApiErrorBody
    } catch {
      body = { error: { message: response.statusText } }
    }
    throw new ApiError(response.status, body)
  }

  return response.json() as Promise<T>
}

function itemPath(
  collection: 'connections' | 'addresses' | 'alert-properties' | 'alert-configs',
  subject: string,
) {
  return `/api/${collection}/${encodeURIComponent(subject)}`
}

export const api = {
  listConnections: () => request<Connection[]>('/api/connections'),
  createConnection: (item: Connection) =>
    request<Connection>('/api/connections', {
      method: 'POST',
      body: JSON.stringify(item),
    }),
  updateConnection: (item: Connection) =>
    request<Connection>(itemPath('connections', item.subject), {
      method: 'PUT',
      body: JSON.stringify(item),
    }),
  disableConnection: (subject: string) =>
    request<Connection>(itemPath('connections', subject), { method: 'DELETE' }),

  listAddresses: () => request<Address[]>('/api/addresses'),
  createAddress: (item: Address) =>
    request<Address>('/api/addresses', {
      method: 'POST',
      body: JSON.stringify(item),
    }),
  updateAddress: (item: Address) =>
    request<Address>(itemPath('addresses', item.subject), {
      method: 'PUT',
      body: JSON.stringify(item),
    }),
  disableAddress: (subject: string) =>
    request<Address>(itemPath('addresses', subject), { method: 'DELETE' }),

  listAlerts: () => request<AlertRecord[]>('/api/alerts'),
  acknowledgeAlert: (subject: string) =>
    request<AlertRecord>(`/api/alerts/${encodeURIComponent(subject)}/acknowledge`, {
      method: 'POST',
    }),

  listAlertProperties: () => request<AlertProperties[]>('/api/alert-properties'),
  createAlertProperties: (item: AlertProperties) =>
    request<AlertProperties>('/api/alert-properties', {
      method: 'POST',
      body: JSON.stringify(item),
    }),
  updateAlertProperties: (item: AlertProperties) =>
    request<AlertProperties>(itemPath('alert-properties', item.subject), {
      method: 'PUT',
      body: JSON.stringify(item),
    }),

  listAlertConfigs: () => request<AlertConfig[]>('/api/alert-configs'),
  createAlertConfig: (item: AlertConfig) =>
    request<AlertConfig>('/api/alert-configs', {
      method: 'POST',
      body: JSON.stringify(item),
    }),
  updateAlertConfig: (item: AlertConfig) =>
    request<AlertConfig>(itemPath('alert-configs', item.subject), {
      method: 'PUT',
      body: JSON.stringify(item),
    }),
  disableAlertConfig: (subject: string) =>
    request<AlertConfig>(itemPath('alert-configs', subject), { method: 'DELETE' }),
}

export function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : 'An unexpected error occurred'
}
