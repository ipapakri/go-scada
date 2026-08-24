import type { Address, AlertRecord, AlertState, LiveEvent } from './models'

export const OPERATOR_PLANT_ID = ''

const plantEquipment = 'tank|valve|pump1|pump2|pumps|utility'
const operatorPlantPattern = new RegExp(`^plant\\.(${plantEquipment})[.]`)
const replicaPlantPattern = new RegExp(`^plant\\.(\\d{3})\\.(${plantEquipment})[.]`)

export function plantIdFromSubject(subject: string) {
  const replica = subject.match(replicaPlantPattern)
  if (replica) return replica[1]
  if (operatorPlantPattern.test(subject)) return OPERATOR_PLANT_ID
  return undefined
}

export function isPlantSubject(subject: string) {
  return plantIdFromSubject(subject) !== undefined
}

export function listPlants(addresses: Address[]) {
  const plants = new Set<string>()
  for (const item of addresses) {
    const plantId = plantIdFromSubject(item.subject)
    if (plantId !== undefined) plants.add(plantId)
  }
  return [...plants].sort((left, right) => {
    if (left === OPERATOR_PLANT_ID) return -1
    if (right === OPERATOR_PLANT_ID) return 1
    return left.localeCompare(right)
  })
}

export function listTankPlants(addresses: Address[]) {
  const plants = listPlants(addresses)
  const numbered = plants.filter((id) => id !== OPERATOR_PLANT_ID)
  return numbered.length > 0 ? numbered : plants
}

export function plantLabel(plantId: string) {
  return plantId === OPERATOR_PLANT_ID ? 'Operator plant' : `Plant ${plantId}`
}

export function plantTelemetry(plantId: string, path: string) {
  return plantId ? `plant.${plantId}.${path}` : `plant.${path}`
}

export function plantAddresses(addresses: Address[], plantId?: string) {
  return addresses.filter((item) => {
    const id = plantIdFromSubject(item.subject)
    return id !== undefined && (plantId === undefined || id === plantId)
  })
}

export function plantAlerts(alerts: AlertRecord[], plantId?: string) {
  return alerts.filter((item) => {
    const id = plantIdFromSubject(item.subject)
    return id !== undefined && (plantId === undefined || id === plantId)
  })
}

export function addressKey(telemetrySubject: string) {
  return `${telemetrySubject}.address`
}

export function readNumber(
  values: Record<string, LiveEvent>,
  telemetrySubject: string,
): number | undefined {
  const event = values[addressKey(telemetrySubject)]
  if (!event || event.type === 'error' || event.value === undefined || event.value === null) {
    return undefined
  }
  const value = typeof event.value === 'number' ? event.value : Number(event.value)
  return Number.isFinite(value) ? value : undefined
}

export function readBool(
  values: Record<string, LiveEvent>,
  telemetrySubject: string,
): boolean | undefined {
  const event = values[addressKey(telemetrySubject)]
  if (!event || event.type === 'error' || event.value === undefined || event.value === null) {
    return undefined
  }
  if (typeof event.value === 'boolean') return event.value
  if (event.value === 'true' || event.value === 1) return true
  if (event.value === 'false' || event.value === 0) return false
  return undefined
}

export function formatNumber(
  value: number | undefined,
  digits = 1,
  unit = '',
) {
  if (value === undefined) return '—'
  const formatted = value.toLocaleString('en-US', {
    minimumFractionDigits: digits,
    maximumFractionDigits: digits,
  })
  return unit ? `${formatted} ${unit}` : formatted
}

export function clampPercent(value: number | undefined) {
  if (value === undefined) return 0
  return Math.min(100, Math.max(0, value))
}

export function alertStatus(state: AlertState | undefined) {
  if (!state) return 'unknown'
  if (state.active) return 'active'
  if (state.pending) return 'pending'
  return 'normal'
}

export function readTank(values: Record<string, LiveEvent>, plantId: string) {
  const point = (path: string) => plantTelemetry(plantId, path)
  return {
    level: readNumber(values, point('tank.level')),
    temperature: readNumber(values, point('tank.temperature')),
    pressure: readNumber(values, point('tank.pressure')),
    high: readBool(values, point('tank.level_high')),
    low: readBool(values, point('tank.level_low')),
    sensorBad: readBool(values, point('tank.sensor_bad')),
  }
}

export function tankIsAlarm(liveStates: Record<string, AlertState>, plantId: string) {
  const point = (path: string) => plantTelemetry(plantId, path)
  return (
    alertStatus(liveStates[`${point('tank')}.alert`]) === 'active' ||
    alertStatus(liveStates[`${point('tank.level_high')}.alert`]) === 'active' ||
    alertStatus(liveStates[`${point('tank.temperature')}.alert`]) === 'active'
  )
}
