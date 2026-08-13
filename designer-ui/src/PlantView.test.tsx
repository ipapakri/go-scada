import { describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { PlantView } from './PlantView'
import {
  alertStatus,
  clampPercent,
  formatNumber,
  plantAddresses,
  readBool,
  readNumber,
} from './plant'
import type { Address, AlertRecord, LiveEvent } from './models'

const tankLevel: Address = {
  subject: 'plant.tank.level.address',
  version: 1,
  driver: 'modbus',
  value_type: 'float64',
  enabled: true,
  connection: 'Modbus.SimulatorTank.config',
  config: { register: 'input', address: 0, encoding: 'float32' },
  telemetry_subject: 'plant.tank.level',
}

const values: Record<string, LiveEvent> = {
  'plant.tank.level.address': {
    type: 'value',
    subject: 'plant.tank.level.address',
    telemetry_subject: 'plant.tank.level',
    value: 61.2,
  },
  'plant.tank.temperature.address': {
    type: 'value',
    subject: 'plant.tank.temperature.address',
    telemetry_subject: 'plant.tank.temperature',
    value: 72.4,
  },
  'plant.pump1.running.address': {
    type: 'value',
    subject: 'plant.pump1.running.address',
    telemetry_subject: 'plant.pump1.running',
    value: true,
  },
  'plant.pump1.trip.address': {
    type: 'value',
    subject: 'plant.pump1.trip.address',
    telemetry_subject: 'plant.pump1.trip',
    value: false,
  },
  'plant.valve.inlet.open.address': {
    type: 'value',
    subject: 'plant.valve.inlet.open.address',
    telemetry_subject: 'plant.valve.inlet.open',
    value: true,
  },
  'plant.valve.inlet.position.address': {
    type: 'value',
    subject: 'plant.valve.inlet.position.address',
    telemetry_subject: 'plant.valve.inlet.position',
    value: 48,
  },
}

const alerts: AlertRecord[] = [
  {
    subject: 'plant.tank.level_high.alert',
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
      text: 'Plant tank level is high',
      acknowledged: false,
      came_time: '2026-08-13T12:00:00Z',
      episode_id: 'episode-1',
    },
  },
]

describe('plant value helpers', () => {
  it('reads numeric and boolean live values', () => {
    expect(readNumber(values, 'plant.tank.level')).toBeCloseTo(61.2)
    expect(readBool(values, 'plant.pump1.running')).toBe(true)
    expect(readBool(values, 'plant.pump1.trip')).toBe(false)
    expect(readNumber(values, 'plant.missing')).toBeUndefined()
  })

  it('formats and clamps plant measurements', () => {
    expect(formatNumber(61.2, 1, '%')).toBe('61.2 %')
    expect(formatNumber(undefined, 1, '%')).toBe('—')
    expect(clampPercent(140)).toBe(100)
    expect(clampPercent(-8)).toBe(0)
    expect(alertStatus(alerts[0].state)).toBe('active')
    expect(plantAddresses([tankLevel]).map((item) => item.subject)).toEqual([
      'plant.tank.level.address',
    ])
  })
})

describe('PlantView', () => {
  it('shows an empty state without simulator addresses', () => {
    render(
      <PlantView
        addresses={[]}
        values={{}}
        alerts={[]}
        liveStates={{}}
        onAcknowledge={vi.fn()}
      />,
    )
    expect(screen.getByText('No plant telemetry yet')).toBeVisible()
  })

  it('renders live process values, statuses, and active alarms', async () => {
    const onAcknowledge = vi.fn().mockResolvedValue(undefined)
    const user = userEvent.setup()
    render(
      <PlantView
        addresses={[tankLevel]}
        values={values}
        alerts={alerts}
        liveStates={{ 'plant.tank.level_high.alert': alerts[0].state }}
        onAcknowledge={onAcknowledge}
      />,
    )

    expect(screen.getByLabelText('Tank level 61.2 %')).toBeVisible()
    expect(screen.getByText('72.4 °C')).toBeVisible()
    expect(screen.getByText('Running')).toBeVisible()
    expect(screen.getByText('Plant tank level is high')).toBeVisible()
    expect(screen.getByText('Open')).toBeVisible()

    await user.click(screen.getByRole('button', { name: 'Acknowledge' }))
    expect(onAcknowledge).toHaveBeenCalledWith('plant.tank.level_high.alert')
  })
})
