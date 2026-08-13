import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import App from './App'
import type { AlertConfig, AlertProperties, AlertRecord, Connection } from './models'

const connections: Connection[] = [
  {
    subject: 'plant.main.config',
    version: 1,
    driver: 'modbus',
    enabled: true,
    config: {
      url: 'tcp://192.168.1.20:502',
      unit_id: 1,
      byte_order: 'big',
      word_order: 'big',
      timeout: '2s',
      poll_interval: '1s',
    },
  },
  {
    subject: 'plant.backup.config',
    version: 1,
    driver: 'modbus',
    enabled: false,
    config: {
      url: 'tcp://192.168.1.21:502',
      unit_id: 2,
      byte_order: 'big',
      word_order: 'big',
      timeout: '2s',
      poll_interval: '1s',
    },
  },
]

const alerts: AlertRecord[] = [
  {
    subject: 'tank.level.alert',
    state: {
      version: 1,
      active: true,
      pending: true,
      property: 'AlertProperties.Alarm',
      color: '#dc2626',
      abbreviation: 'ALM',
      short_sign: '!',
      priority: 10,
      requires_acknowledgement: true,
      text: 'Tank level is high',
      acknowledged: false,
      came_time: '2026-08-13T12:00:00Z',
      episode_id: 'episode-1',
    },
  },
]

const properties: AlertProperties[] = [
  {
    subject: 'AlertProperties.Alarm',
    version: 1,
    color: '#dc2626',
    abbreviation: 'ALM',
    short_sign: '!',
    priority: 10,
    requires_acknowledgement: true,
    reference_count: 1,
  },
]

const alertConfigs: AlertConfig[] = [
  {
    subject: 'tank.level.alert_config',
    version: 1,
    enabled: true,
    type: 'binary',
    binary: {
      bad_value: true,
      true: { property: 'AlertProperties.Alarm', text: 'Tank level is high' },
      false: { text: 'Tank level is normal' },
    },
    input_subject: 'tank.level',
    output_subject: 'tank.level.alert',
  },
]

function jsonResponse(value: unknown, status = 200) {
  return new Response(JSON.stringify(value), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

beforeEach(() => {
  vi.stubGlobal('WebSocket', undefined)
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input)
      if (path === '/api/connections') return jsonResponse(connections)
      if (path === '/api/addresses') return jsonResponse([])
      if (path === '/api/alerts') return jsonResponse(alerts)
      if (path === '/api/alert-properties') return jsonResponse(properties)
      if (path === '/api/alert-configs') return jsonResponse(alertConfigs)
      throw new Error(`Unexpected request: ${path}`)
    }),
  )
})

describe('Modbus Designer', () => {
  it('opens on the plant overview', async () => {
    render(<App />)
    expect(await screen.findByRole('heading', { name: 'Plant overview' })).toBeVisible()
    expect(screen.getByText('No plant telemetry yet')).toBeVisible()
  })

  it('uses enabled connections and enforces register encoding constraints', async () => {
    const user = userEvent.setup()
    render(<App />)

    await user.click(await screen.findByRole('button', { name: /Addresses/ }))
    await user.click(screen.getByRole('button', { name: /Add address/ }))

    const connectionSelect = screen.getByLabelText('Connection')
    expect(within(connectionSelect).getByRole('option', { name: 'plant.main.config' })).toBeVisible()
    expect(
      within(connectionSelect).queryByRole('option', { name: /plant.backup.config/ }),
    ).not.toBeInTheDocument()

    await user.selectOptions(screen.getByLabelText('Register'), 'coil')
    const encodingSelect = screen.getByLabelText('Encoding')
    expect(within(encodingSelect).getAllByRole('option')).toHaveLength(1)
    expect(encodingSelect).toHaveValue('bool')
    expect(screen.getByText('Value type').parentElement).toHaveTextContent('bool')

    await user.type(
      screen.getByPlaceholderText('plant.line1.temperature.address'),
      'plant.line1.run.address',
    )
    expect(screen.getByText('plant.line1.run')).toBeVisible()
  })

  it('shows structured API errors inside the editor', async () => {
    const user = userEvent.setup()
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockImplementation(async (input, init) => {
      const path = String(input)
      if (path === '/api/connections' && !init?.method) return jsonResponse(connections)
      if (path === '/api/addresses') return jsonResponse([])
      if (path === '/api/alerts') return jsonResponse(alerts)
      if (path === '/api/alert-properties') return jsonResponse(properties)
      if (path === '/api/alert-configs') return jsonResponse(alertConfigs)
      if (path === '/api/connections' && init?.method === 'POST') {
        return jsonResponse(
          {
            error: {
              code: 'validation_failed',
              message: 'connection is invalid',
              fields: { connection: 'poll_interval must be positive' },
            },
          },
          422,
        )
      }
      throw new Error(`Unexpected request: ${path}`)
    })

    render(<App />)
    await user.click(await screen.findByRole('button', { name: /Connections/ }))
    await screen.findByText('plant.main.config')
    await user.click(screen.getByRole('button', { name: /Add connection/ }))
    const subject = screen.getByPlaceholderText('plant.line1.plc.config')
    await user.type(subject, 'plant.new.config')
    await user.click(screen.getByRole('button', { name: 'Create connection' }))

    await waitFor(() =>
      expect(screen.getByRole('alert')).toHaveTextContent('poll_interval must be positive'),
    )
  })

  it('shows current alarms and acknowledges a pending episode', async () => {
    const user = userEvent.setup()
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockImplementation(async (input, init) => {
      const path = String(input)
      if (path === '/api/connections') return jsonResponse(connections)
      if (path === '/api/addresses') return jsonResponse([])
      if (path === '/api/alerts') return jsonResponse(alerts)
      if (path === '/api/alert-properties') return jsonResponse(properties)
      if (path === '/api/alert-configs') return jsonResponse(alertConfigs)
      if (path === '/api/alerts/tank.level.alert/acknowledge' && init?.method === 'POST') {
        return jsonResponse({
          ...alerts[0],
          state: { ...alerts[0].state, acknowledged: true },
        })
      }
      throw new Error(`Unexpected request: ${path}`)
    })

    render(<App />)
    await user.click(await screen.findByRole('button', { name: /Alarms/ }))
    expect(await screen.findByText('Tank level is high')).toBeVisible()
    await user.click(screen.getByRole('button', { name: 'Acknowledge' }))

    await waitFor(() => expect(screen.getByText('Acknowledged')).toBeVisible())
    expect(fetchMock).toHaveBeenCalledWith(
      '/api/alerts/tank.level.alert/acknowledge',
      expect.objectContaining({ method: 'POST' }),
    )
  })

  it('creates guided alarm properties', async () => {
    const user = userEvent.setup()
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockImplementation(async (input, init) => {
      const path = String(input)
      if (path === '/api/connections') return jsonResponse(connections)
      if (path === '/api/addresses') return jsonResponse([])
      if (path === '/api/alerts') return jsonResponse(alerts)
      if (path === '/api/alert-properties' && !init?.method) return jsonResponse(properties)
      if (path === '/api/alert-configs') return jsonResponse(alertConfigs)
      if (path === '/api/alert-properties' && init?.method === 'POST') {
        return jsonResponse(JSON.parse(String(init.body)), 201)
      }
      throw new Error(`Unexpected request: ${path}`)
    })

    render(<App />)
    await user.click(await screen.findByRole('button', { name: /Alert properties/ }))
    await user.click(screen.getByRole('button', { name: /Add properties/ }))
    const subject = screen.getByPlaceholderText('AlertProperties.Alarm')
    await user.clear(subject)
    await user.type(subject, 'AlertProperties.Warning')
    await user.clear(screen.getByLabelText('Abbreviation'))
    await user.type(screen.getByLabelText('Abbreviation'), 'WRN')
    await user.click(screen.getByRole('button', { name: 'Create properties' }))

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        '/api/alert-properties',
        expect.objectContaining({ method: 'POST' }),
      ),
    )
    expect(await screen.findByText('AlertProperties.Warning')).toBeVisible()
  })

  it('builds a contiguous value alarm definition', async () => {
    const user = userEvent.setup()
    const fetchMock = vi.mocked(fetch)
    fetchMock.mockImplementation(async (input, init) => {
      const path = String(input)
      if (path === '/api/connections') return jsonResponse(connections)
      if (path === '/api/addresses') return jsonResponse([])
      if (path === '/api/alerts') return jsonResponse(alerts)
      if (path === '/api/alert-properties') return jsonResponse(properties)
      if (path === '/api/alert-configs' && !init?.method) return jsonResponse(alertConfigs)
      if (path === '/api/alert-configs' && init?.method === 'POST') {
        const body = JSON.parse(String(init.body)) as AlertConfig
        return jsonResponse({
          ...body,
          input_subject: 'tank.temperature',
          output_subject: 'tank.temperature.alert',
        }, 201)
      }
      throw new Error(`Unexpected request: ${path}`)
    })

    render(<App />)
    await user.click(await screen.findByRole('button', { name: /Alert definitions/ }))
    await user.click(screen.getByRole('button', { name: /Add definition/ }))
    await user.selectOptions(screen.getByLabelText('Definition type'), 'value')
    await user.type(
      screen.getByPlaceholderText('tank.temperature.alert_config'),
      'tank.temperature.alert_config',
    )
    const boundary = screen.getByLabelText('Interval 1 maximum')
    await user.clear(boundary)
    await user.type(boundary, '80')
    await user.click(screen.getByRole('button', { name: 'Create definition' }))

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        '/api/alert-configs',
        expect.objectContaining({ method: 'POST' }),
      ),
    )
    const createCall = fetchMock.mock.calls.find(
      ([path, init]) => String(path) === '/api/alert-configs' && init?.method === 'POST',
    )
    const submitted = JSON.parse(String(createCall?.[1]?.body)) as AlertConfig
    expect(submitted.value?.intervals).toEqual([
      expect.objectContaining({ min: null, max: 80 }),
      expect.objectContaining({ min: 80, max: null }),
    ])
  })
})
