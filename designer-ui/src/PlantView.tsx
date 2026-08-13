import { useMemo, useState } from 'react'
import { errorMessage } from './api'
import type { Address, AlertRecord, AlertState, LiveEvent } from './models'
import {
  alertStatus,
  clampPercent,
  formatNumber,
  plantAddresses,
  plantAlerts,
  readBool,
  readNumber,
} from './plant'

interface PlantViewProps {
  addresses: Address[]
  values: Record<string, LiveEvent>
  alerts: AlertRecord[]
  liveStates: Record<string, AlertState>
  onAcknowledge: (subject: string) => Promise<void>
}

function lampClass(value: boolean | undefined, kind: 'status' | 'fault') {
  if (value === undefined) return 'unknown'
  if (kind === 'fault') return value ? 'fault' : 'ok'
  return value ? 'on' : 'off'
}

function Lamp({
  label,
  value,
  kind = 'status',
}: {
  label: string
  value: boolean | undefined
  kind?: 'status' | 'fault'
}) {
  return (
    <span className={`plant-lamp ${lampClass(value, kind)}`}>
      <span />
      {label}
    </span>
  )
}

function Metric({
  label,
  value,
  digits = 1,
  unit = '',
}: {
  label: string
  value: number | undefined
  digits?: number
  unit?: string
}) {
  return (
    <div className="plant-metric">
      <span>{label}</span>
      <strong>{formatNumber(value, digits, unit)}</strong>
    </div>
  )
}

function Valve({
  name,
  open,
  position,
}: {
  name: string
  open: boolean | undefined
  position: number | undefined
}) {
  const fill = clampPercent(position)
  return (
    <article className={`plant-subcard valve ${open ? 'open' : ''}`}>
      <header>
        <h3>{name}</h3>
        <Lamp label={open ? 'Open' : open === false ? 'Closed' : 'Unknown'} value={open} />
      </header>
      <div className="valve-track" aria-label={`${name} position`}>
        <div className="valve-fill" style={{ width: `${fill}%` }} />
      </div>
      <Metric label="Position" value={position} unit="%" />
    </article>
  )
}

function Pump({
  name,
  running,
  trip,
  speed,
  current,
}: {
  name: string
  running: boolean | undefined
  trip: boolean | undefined
  speed: number | undefined
  current: number | undefined
}) {
  return (
    <article className={`plant-subcard pump ${running ? 'running' : ''} ${trip ? 'tripped' : ''}`}>
      <header>
        <div className={`pump-rotor ${running ? 'spinning' : ''}`} aria-hidden="true">
          <span />
        </div>
        <div>
          <h3>{name}</h3>
          <div className="plant-lamps">
            <Lamp label={running ? 'Running' : 'Stopped'} value={running} />
            <Lamp label="Trip" value={trip} kind="fault" />
          </div>
        </div>
      </header>
      <div className="plant-metrics">
        <Metric label="Speed" value={speed} unit="%" />
        <Metric label="Current" value={current} unit="A" />
      </div>
    </article>
  )
}

export function PlantView({
  addresses,
  values,
  alerts,
  liveStates,
  onAcknowledge,
}: PlantViewProps) {
  const [busy, setBusy] = useState('')
  const [error, setError] = useState<unknown>(null)
  const hasPlant = plantAddresses(addresses).length > 0
  const configuredAlerts = plantAlerts(alerts)

  const tank = {
    level: readNumber(values, 'plant.tank.level'),
    temperature: readNumber(values, 'plant.tank.temperature'),
    pressure: readNumber(values, 'plant.tank.pressure'),
    high: readBool(values, 'plant.tank.level_high'),
    low: readBool(values, 'plant.tank.level_low'),
    sensorBad: readBool(values, 'plant.tank.sensor_bad'),
  }
  const inlet = {
    open: readBool(values, 'plant.valve.inlet.open'),
    position: readNumber(values, 'plant.valve.inlet.position'),
  }
  const outlet = {
    open: readBool(values, 'plant.valve.outlet.open'),
    position: readNumber(values, 'plant.valve.outlet.position'),
  }
  const cooling = {
    open: readBool(values, 'plant.valve.cooling.open'),
    position: readNumber(values, 'plant.valve.cooling.position'),
  }
  const pump1 = {
    running: readBool(values, 'plant.pump1.running'),
    trip: readBool(values, 'plant.pump1.trip'),
    speed: readNumber(values, 'plant.pump1.speed'),
    current: readNumber(values, 'plant.pump1.current'),
  }
  const pump2 = {
    running: readBool(values, 'plant.pump2.running'),
    trip: readBool(values, 'plant.pump2.trip'),
    speed: readNumber(values, 'plant.pump2.speed'),
    current: readNumber(values, 'plant.pump2.current'),
  }
  const pumps = {
    pressure: readNumber(values, 'plant.pumps.discharge_pressure'),
    flow: readNumber(values, 'plant.pumps.total_flow'),
    vibration: readNumber(values, 'plant.pumps.vibration'),
  }
  const utility = {
    flow: readNumber(values, 'plant.utility.process_flow'),
    ambient: readNumber(values, 'plant.utility.ambient_temperature'),
    conductivity: readNumber(values, 'plant.utility.conductivity'),
    temperatureHigh: readBool(values, 'plant.utility.temperature_high'),
    flowLow: readBool(values, 'plant.utility.flow_low'),
  }

  const tankLevelAlert = liveStates['plant.tank.level_high.alert']
  const tankTempAlert = liveStates['plant.tank.temperature.alert']
  const tankSummary = liveStates['plant.tank.alert']
  const tankAlarm = alertStatus(tankSummary) === 'active' ||
    alertStatus(tankLevelAlert) === 'active' ||
    alertStatus(tankTempAlert) === 'active'

  const openAlarms = useMemo(
    () =>
      configuredAlerts
        .map((item) => ({ ...item, state: liveStates[item.subject] ?? item.state }))
        .filter((item) => item.state.active || item.state.pending)
        .sort((left, right) => right.state.priority - left.state.priority),
    [configuredAlerts, liveStates],
  )

  const acknowledge = async (subject: string) => {
    setBusy(subject)
    setError(null)
    try {
      await onAcknowledge(subject)
    } catch (ackError) {
      setError(ackError)
    } finally {
      setBusy('')
    }
  }

  if (!hasPlant) {
    return (
      <div className="empty-state">
        <strong>No plant telemetry yet</strong>
        <span>Start the simulation profile to seed tank, pump, and utility points.</span>
      </div>
    )
  }

  return (
    <section className="plant-view" aria-label="Plant overview">
      {Boolean(error) && (
        <div className="page-error" role="alert">
          <span>{errorMessage(error)}</span>
          <button type="button" onClick={() => setError(null)}>Dismiss</button>
        </div>
      )}

      {openAlarms.length > 0 && (
        <div className="plant-alarms" aria-label="Plant alarms">
          {openAlarms.map(({ subject, state }) => (
            <article
              key={subject}
              className={`plant-alarm ${state.active ? 'active' : 'pending'}`}
            >
              <span
                className="alarm-color"
                style={{ backgroundColor: state.color || '#be123c' }}
                aria-hidden="true"
              />
              <div>
                <strong>{state.text || subject}</strong>
                <small>{state.active ? 'Active' : 'Cleared · unacknowledged'}</small>
              </div>
              {state.pending && !state.acknowledged ? (
                <button
                  className="button acknowledge"
                  type="button"
                  disabled={busy === subject}
                  onClick={() => void acknowledge(subject)}
                >
                  {busy === subject ? 'Acknowledging…' : 'Acknowledge'}
                </button>
              ) : (
                <span className="acknowledged">
                  {state.acknowledged ? 'Acknowledged' : 'No action'}
                </span>
              )}
            </article>
          ))}
        </div>
      )}

      <div className="plant-flow">
        <Valve name="Inlet valve" open={inlet.open} position={inlet.position} />
        <div className="plant-pipe" aria-hidden="true" />
        <article className={`plant-card tank ${tankAlarm ? 'alarm' : ''}`}>
          <header>
            <h3>Process tank</h3>
            <div className="plant-lamps">
              <Lamp label="High" value={tank.high} kind="fault" />
              <Lamp label="Low" value={tank.low} kind="fault" />
              <Lamp label="Sensor" value={tank.sensorBad} kind="fault" />
            </div>
          </header>
          <div className="tank-body">
            <div
              className="tank-shell"
              role="img"
              aria-label={`Tank level ${formatNumber(tank.level, 1, '%')}`}
            >
              <div
                className="tank-fill"
                style={{ height: `${clampPercent(tank.level)}%` }}
              />
              <strong>{formatNumber(tank.level, 1, '%')}</strong>
            </div>
            <div className="plant-metrics stacked">
              <Metric label="Temperature" value={tank.temperature} unit="°C" />
              <Metric label="Pressure" value={tank.pressure} unit="bar" />
            </div>
          </div>
        </article>
        <div className="plant-pipe" aria-hidden="true" />
        <Valve name="Outlet valve" open={outlet.open} position={outlet.position} />
      </div>

      <div className="plant-grid">
        <article className="plant-card pumps">
          <header>
            <h3>Transfer pumps</h3>
            <div className="plant-metrics compact">
              <Metric label="Discharge" value={pumps.pressure} unit="bar" />
              <Metric label="Total flow" value={pumps.flow} unit="m³/h" />
              <Metric label="Vibration" value={pumps.vibration} unit="mm/s" />
            </div>
          </header>
          <div className="pump-row">
            <Pump
              name="Pump 1"
              running={pump1.running}
              trip={pump1.trip}
              speed={pump1.speed}
              current={pump1.current}
            />
            <Pump
              name="Pump 2"
              running={pump2.running}
              trip={pump2.trip}
              speed={pump2.speed}
              current={pump2.current}
            />
          </div>
        </article>

        <article className="plant-card utility">
          <header>
            <h3>Utility</h3>
            <div className="plant-lamps">
              <Lamp label="High temperature" value={utility.temperatureHigh} kind="fault" />
              <Lamp label="Low flow" value={utility.flowLow} kind="fault" />
            </div>
          </header>
          <div className="plant-metrics">
            <Metric label="Process flow" value={utility.flow} unit="m³/h" />
            <Metric label="Ambient" value={utility.ambient} unit="°C" />
            <Metric label="Conductivity" value={utility.conductivity} digits={0} unit="µS/cm" />
          </div>
          <Valve name="Cooling valve" open={cooling.open} position={cooling.position} />
        </article>
      </div>
    </section>
  )
}
