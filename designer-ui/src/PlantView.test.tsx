import { describe, expect, it, vi } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { PlantView } from './PlantView'
import {
  alertStatus,
  clampPercent,
  formatNumber,
  listPlants,
  OPERATOR_PLANT_ID,
  plantAddresses,
  plantLabel,
  plantTelemetry,
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
  publish_on_change: false,
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
    expect(
      plantAddresses([
        tankLevel,
        { ...tankLevel, subject: 'plant.001.tank.level.address' },
        { ...tankLevel, subject: 'plant.line1.temperature.address' },
      ]).map((item) => item.subject),
    ).toEqual(['plant.tank.level.address', 'plant.001.tank.level.address'])
    expect(
      plantAddresses(
        [tankLevel, { ...tankLevel, subject: 'plant.001.tank.level.address' }],
        '001',
      ).map((item) => item.subject),
    ).toEqual(['plant.001.tank.level.address'])
    expect(
      listPlants([
        { ...tankLevel, subject: 'plant.002.tank.level.address' },
        tankLevel,
        { ...tankLevel, subject: 'plant.001.tank.level.address' },
      ]),
    ).toEqual([OPERATOR_PLANT_ID, '001', '002'])
    expect(plantLabel(OPERATOR_PLANT_ID)).toBe('Operator plant')
    expect(plantLabel('001')).toBe('Plant 001')
    expect(plantTelemetry(OPERATOR_PLANT_ID, 'tank.level')).toBe('plant.tank.level')
    expect(plantTelemetry('001', 'tank.level')).toBe('plant.001.tank.level')
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
        selectedPlantId={OPERATOR_PLANT_ID}
        onSelectPlant={vi.fn()}
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
        selectedPlantId={OPERATOR_PLANT_ID}
        onSelectPlant={vi.fn()}
        onAcknowledge={onAcknowledge}
      />,
    )

    expect(screen.getByLabelText('Tank level 61.2 %')).toBeVisible()
    expect(screen.getByText('72.4 °C')).toBeVisible()
    expect(screen.getByText('Running')).toBeVisible()
    expect(screen.getByText('Plant tank level is high')).toBeVisible()
    expect(screen.getByText('Open')).toBeVisible()
    expect(screen.getByLabelText('Plant')).toHaveValue(OPERATOR_PLANT_ID)

    await user.click(screen.getByRole('button', { name: 'Acknowledge' }))
    expect(onAcknowledge).toHaveBeenCalledWith('plant.tank.level_high.alert')
  })

  it('switches live values and alarms when another plant is selected', async () => {
    const onSelectPlant = vi.fn()
    const user = userEvent.setup()
    const replicaLevel: Address = {
      ...tankLevel,
      subject: 'plant.001.tank.level.address',
      telemetry_subject: 'plant.001.tank.level',
    }
    const replicaValues: Record<string, LiveEvent> = {
      ...values,
      'plant.001.tank.level.address': {
        type: 'value',
        subject: 'plant.001.tank.level.address',
        telemetry_subject: 'plant.001.tank.level',
        value: 33.5,
      },
    }
    const replicaAlerts: AlertRecord[] = [
      {
        ...alerts[0],
        subject: 'plant.001.tank.level_high.alert',
        state: {
          ...alerts[0].state,
          text: 'Plant 001 tank level is high',
        },
      },
    ]

    const { rerender } = render(
      <PlantView
        addresses={[tankLevel, replicaLevel]}
        values={replicaValues}
        alerts={[...alerts, ...replicaAlerts]}
        liveStates={{
          'plant.tank.level_high.alert': alerts[0].state,
          'plant.001.tank.level_high.alert': replicaAlerts[0].state,
        }}
        selectedPlantId={OPERATOR_PLANT_ID}
        onSelectPlant={onSelectPlant}
        onAcknowledge={vi.fn()}
      />,
    )

    const plantSelect = screen.getByLabelText('Plant')
    expect(within(plantSelect).getByRole('option', { name: 'Operator plant' })).toBeVisible()
    expect(within(plantSelect).getByRole('option', { name: 'Plant 001' })).toBeVisible()
    expect(screen.getByLabelText('Tank level 61.2 %')).toBeVisible()
    expect(screen.getByText('Plant tank level is high')).toBeVisible()

    await user.selectOptions(plantSelect, '001')
    expect(onSelectPlant).toHaveBeenCalledWith('001')

    rerender(
      <PlantView
        addresses={[tankLevel, replicaLevel]}
        values={replicaValues}
        alerts={[...alerts, ...replicaAlerts]}
        liveStates={{
          'plant.tank.level_high.alert': alerts[0].state,
          'plant.001.tank.level_high.alert': replicaAlerts[0].state,
        }}
        selectedPlantId="001"
        onSelectPlant={onSelectPlant}
        onAcknowledge={vi.fn()}
      />,
    )

    expect(screen.getByLabelText('Tank level 33.5 %')).toBeVisible()
    expect(screen.getByText('Plant 001 tank level is high')).toBeVisible()
    expect(screen.queryByText('Plant tank level is high')).not.toBeInTheDocument()
  })
})
