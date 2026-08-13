import type { Address, AlertRecord, AlertState, LiveEvent } from './models'

export const PLANT_PREFIX = 'plant.'

export function isPlantSubject(subject: string) {
  return subject.startsWith(PLANT_PREFIX)
}

export function plantAddresses(addresses: Address[]) {
  return addresses.filter((item) => isPlantSubject(item.subject))
}

export function plantAlerts(alerts: AlertRecord[]) {
  return alerts.filter((item) => isPlantSubject(item.subject))
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
