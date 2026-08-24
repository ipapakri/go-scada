'use strict'

const DEFAULT_INSTANCE_COUNT = 10
const MAX_INSTANCE_COUNT = 999

function parseInstanceCount (value, fallback = DEFAULT_INSTANCE_COUNT) {
  const parsed = Number(value)
  if (!Number.isInteger(parsed) || parsed < 1) return fallback
  return Math.min(parsed, MAX_INSTANCE_COUNT)
}

const INSTANCE_COUNT = parseInstanceCount(process.env.SIMULATOR_INSTANCES)

const REGISTER_MAP = Object.freeze({
  tank: Object.freeze({
    port: 1502,
    unitId: 1,
    count: INSTANCE_COUNT,
    floatStride: 20,
    discreteStride: 3,
    coilStride: 2,
    floats: Object.freeze({
      level: 0,
      temperature: 4,
      pressure: 8,
      inletPosition: 12,
      outletPosition: 16
    }),
    discrete: Object.freeze({ levelHigh: 0, levelLow: 1, sensorBad: 2 }),
    coils: Object.freeze({ inletOpen: 0, outletOpen: 1 })
  }),
  pumps: Object.freeze({
    port: 1503,
    unitId: 2,
    count: INSTANCE_COUNT,
    floatStride: 28,
    discreteStride: 4,
    coilStride: 0,
    floats: Object.freeze({
      pump1Speed: 0,
      pump1Current: 4,
      dischargePressure: 8,
      pump2Speed: 12,
      pump2Current: 16,
      totalFlow: 20,
      vibration: 24
    }),
    discrete: Object.freeze({
      pump1Running: 0,
      pump1Trip: 1,
      pump2Running: 2,
      pump2Trip: 3
    })
  }),
  utility: Object.freeze({
    port: 1504,
    unitId: 3,
    count: INSTANCE_COUNT,
    floatStride: 16,
    discreteStride: 2,
    coilStride: 1,
    floats: Object.freeze({
      processFlow: 0,
      ambientTemperature: 4,
      conductivity: 8,
      coolingValvePosition: 12
    }),
    discrete: Object.freeze({ temperatureHigh: 0, flowLow: 1 }),
    coils: Object.freeze({ coolingValveOpen: 0 })
  })
})

const DEFAULT_CONTROLS = Object.freeze({
  automatic: true,
  inletCommand: 70,
  outletCommand: 45,
  coolingCommand: 40,
  pump1Command: true,
  pump2Command: false
})

const DEFAULT_FAULTS = Object.freeze({
  pump1Trip: false,
  pump2Trip: false,
  inletStuck: false,
  outletStuck: false,
  highTemperature: false,
  sensorFreeze: false,
  sensorBad: false
})

const BOOLEAN_CONTROLS = new Set(['automatic', 'pump1Command', 'pump2Command'])
const NUMERIC_CONTROLS = new Set(['inletCommand', 'outletCommand', 'coolingCommand'])

function clamp (value, min, max) {
  return Math.min(max, Math.max(min, value))
}

function selectedIndex (state) {
  const count = state.plants.length
  return clamp(Math.round(Number(state.selectedInstance) || 0), 0, count - 1)
}

function selectedPlant (state) {
  return state.plants[selectedIndex(state)]
}

function floatAddress (slaveName, index, field) {
  const slave = REGISTER_MAP[slaveName]
  return index * slave.floatStride + slave.floats[field]
}

function discreteAddress (slaveName, index, field) {
  const slave = REGISTER_MAP[slaveName]
  return index * slave.discreteStride + slave.discrete[field]
}

function coilAddress (slaveName, index, field) {
  const slave = REGISTER_MAP[slaveName]
  return index * slave.coilStride + slave.coils[field]
}

function initialPlant (index) {
  return {
    id: index + 1,
    tick: 0,
    seed: (0x5ca1ab1e ^ Math.imul(index + 1, 0x9e3779b9)) >>> 0,
    level: 55,
    temperature: 24,
    pressure: 2.1,
    inletPosition: 70,
    outletPosition: 45,
    coolingValvePosition: 40,
    pump1Speed: 72,
    pump2Speed: 0,
    pump1Current: 18,
    pump2Current: 0,
    dischargePressure: 3.4,
    totalFlow: 42,
    vibration: 1.2,
    ambientTemperature: 21,
    conductivity: 480,
    controls: { ...DEFAULT_CONTROLS },
    faults: { ...DEFAULT_FAULTS },
    frozenMeasurements: null
  }
}

function initialState (count = INSTANCE_COUNT) {
  const plants = Math.max(1, Math.min(MAX_INSTANCE_COUNT, Number(count) || INSTANCE_COUNT))
  return {
    selectedInstance: 0,
    speed: 1,
    plants: Array.from({ length: plants }, (_, index) => initialPlant(index))
  }
}

function nextNoise (rng, amplitude) {
  rng.seed = (Math.imul(rng.seed, 1664525) + 1013904223) >>> 0
  return ((rng.seed / 0xffffffff) * 2 - 1) * amplitude
}

function approach (value, target, amount) {
  return value + (target - value) * clamp(amount, 0, 1)
}

function stepPlant (plant, dt) {
  if (plant.controls.automatic) {
    const levelError = 60 - plant.level
    plant.controls.inletCommand = clamp(58 + levelError * 1.8, 5, 100)
    plant.controls.outletCommand = clamp(42 - levelError * 0.7, 5, 90)
    plant.controls.pump1Command = plant.level > 18
    plant.controls.pump2Command = plant.level > 72
    plant.controls.coolingCommand = clamp(35 + (plant.temperature - 25) * 5, 10, 100)
  }

  if (!plant.faults.inletStuck) {
    plant.inletPosition = approach(plant.inletPosition, plant.controls.inletCommand, 0.18 * dt)
  }
  if (!plant.faults.outletStuck) {
    plant.outletPosition = approach(plant.outletPosition, plant.controls.outletCommand, 0.18 * dt)
  }
  plant.coolingValvePosition = approach(
    plant.coolingValvePosition,
    plant.controls.coolingCommand,
    0.15 * dt
  )

  const pump1Running = Boolean(
    plant.controls.pump1Command && !plant.faults.pump1Trip && plant.level > 3
  )
  const pump2Running = Boolean(
    plant.controls.pump2Command && !plant.faults.pump2Trip && plant.level > 3
  )
  plant.pump1Speed = approach(plant.pump1Speed, pump1Running ? 72 : 0, 0.25 * dt)
  plant.pump2Speed = approach(plant.pump2Speed, pump2Running ? 65 : 0, 0.25 * dt)

  const inletFlow = plant.inletPosition * 0.42
  const valveOutflow = plant.outletPosition * 0.16
  const pumpedFlow = plant.pump1Speed * 0.36 + plant.pump2Speed * 0.32
  plant.totalFlow = Math.max(0, valveOutflow + pumpedFlow + nextNoise(plant, 0.25))
  plant.level = clamp(plant.level + (inletFlow - plant.totalFlow) * 0.018 * dt, 0, 100)

  const temperatureTarget = plant.faults.highTemperature
    ? 96
    : plant.ambientTemperature + 3 + (pump1Running ? 2.5 : 0) + (pump2Running ? 2 : 0)
  const cooling = plant.coolingValvePosition * 0.018
  plant.temperature = clamp(
    plant.temperature + (temperatureTarget - plant.temperature) * 0.012 * dt - cooling * 0.025 * dt,
    -20,
    130
  )
  plant.pressure = clamp(0.8 + plant.level * 0.027 + pumpedFlow * 0.035 + nextNoise(plant, 0.015), 0, 10)
  plant.dischargePressure = clamp(0.6 + pumpedFlow * 0.075 + nextNoise(plant, 0.02), 0, 12)
  plant.pump1Current = Math.max(0, plant.pump1Speed * 0.25 + nextNoise(plant, 0.08))
  plant.pump2Current = Math.max(0, plant.pump2Speed * 0.27 + nextNoise(plant, 0.08))
  plant.vibration = Math.max(0, 0.25 + (plant.pump1Speed + plant.pump2Speed) * 0.009 + nextNoise(plant, 0.03))
  plant.ambientTemperature = clamp(21 + Math.sin(plant.tick / 90) * 2, 15, 30)
  plant.conductivity = clamp(470 + plant.level * 0.25 + nextNoise(plant, 1.2), 0, 2000)
  plant.tick += 1
  return plant
}

function step (previous, elapsedSeconds = 1) {
  const state = previous || initialState()
  const dt = clamp(Number(elapsedSeconds) || 1, 0.05, 10) *
    clamp(Number(state.speed) || 1, 0.1, 10)
  for (const plant of state.plants) stepPlant(plant, dt)
  return state
}

function applyControl (state, topic, payload) {
  const target = state || initialState()
  const booleanValue = payload === true || payload === 'true' || payload === 1 || payload === '1'
  const numericValue = Number(payload)
  const plant = selectedPlant(target)

  if (topic === 'selectedInstance' || topic === 'selectedTank') {
    if (Number.isFinite(numericValue)) {
      target.selectedInstance = clamp(Math.round(numericValue), 0, target.plants.length - 1)
    }
  } else if (topic === 'speed') {
    if (Number.isFinite(numericValue)) target.speed = clamp(numericValue, 0.1, 10)
  } else if (BOOLEAN_CONTROLS.has(topic)) {
    plant.controls[topic] = booleanValue
  } else if (NUMERIC_CONTROLS.has(topic) && Number.isFinite(numericValue)) {
    plant.controls[topic] = clamp(numericValue, 0, 100)
  } else if (Object.hasOwn(DEFAULT_FAULTS, topic)) {
    plant.faults[topic] = booleanValue
    if (topic === 'sensorFreeze') {
      plant.frozenMeasurements = booleanValue ? plantMeasurements(plant, false) : null
    }
  } else if (topic === 'resetFaults') {
    plant.faults = { ...DEFAULT_FAULTS }
    plant.frozenMeasurements = null
  }
  return target
}

function plantMeasurements (plant, applySensorFaults = true) {
  if (applySensorFaults && plant.faults.sensorFreeze && plant.frozenMeasurements) {
    return { ...plant.frozenMeasurements, sensorBad: false }
  }
  const values = {
    id: plant.id,
    level: plant.level,
    temperature: plant.temperature,
    pressure: plant.pressure,
    inletPosition: plant.inletPosition,
    outletPosition: plant.outletPosition,
    pump1Speed: plant.pump1Speed,
    pump1Current: plant.pump1Current,
    dischargePressure: plant.dischargePressure,
    pump2Speed: plant.pump2Speed,
    pump2Current: plant.pump2Current,
    totalFlow: plant.totalFlow,
    vibration: plant.vibration,
    ambientTemperature: plant.ambientTemperature,
    conductivity: plant.conductivity,
    coolingValvePosition: plant.coolingValvePosition,
    sensorBad: plant.faults.sensorBad
  }
  if (applySensorFaults && plant.faults.sensorBad) {
    values.level = 999.9
    values.temperature = 999.9
  }
  return values
}

function plantStatus (plant) {
  return {
    levelHigh: plant.level >= 80,
    levelLow: plant.level <= 20,
    pump1Running: plant.pump1Speed > 5 && !plant.faults.pump1Trip,
    pump2Running: plant.pump2Speed > 5 && !plant.faults.pump2Trip,
    temperatureHigh: plant.temperature >= 80,
    flowLow: plant.totalFlow < 10
  }
}

function measurements (state, applySensorFaults = true) {
  return {
    plants: (state || initialState()).plants.map(plant => plantMeasurements(plant, applySensorFaults))
  }
}

function encodeFloat32 (value) {
  const buffer = Buffer.allocUnsafe(4)
  buffer.writeFloatBE(Number(value), 0)
  return [buffer.readUInt16BE(0), buffer.readUInt16BE(2)]
}

function floatWrites (register, address, value) {
  const buffer = Buffer.allocUnsafe(4)
  buffer.writeFloatBE(Number(value), 0)
  return {
    payload: {
      register,
      address: address / 4,
      value: Array.from(buffer)
    }
  }
}

function bitFieldWrite (register, values) {
  const bytes = []
  for (let index = 0; index < values.length; index++) {
    const byteIndex = Math.floor(index / 8)
    bytes[byteIndex] = bytes[byteIndex] || 0
    if (values[index]) bytes[byteIndex] |= 1 << (index % 8)
  }
  return { payload: { register, address: 0, value: bytes } }
}

function messagesForSlaves (state) {
  const source = state || initialState()
  const published = measurements(source)
  const tank = []
  const tankDiscrete = []
  const tankCoils = []
  const pumps = []
  const pumpDiscrete = []
  const utility = []
  const utilityDiscrete = []
  const utilityCoils = []

  for (let index = 0; index < source.plants.length; index++) {
    const plant = source.plants[index]
    const value = published.plants[index]
    const status = plantStatus(plant)

    for (const name of Object.keys(REGISTER_MAP.tank.floats)) {
      tank.push(floatWrites('input', floatAddress('tank', index, name), value[name]))
    }
    tankDiscrete.push(status.levelHigh, status.levelLow, value.sensorBad)
    tankCoils.push(plant.inletPosition > 5, plant.outletPosition > 5)

    for (const name of Object.keys(REGISTER_MAP.pumps.floats)) {
      pumps.push(floatWrites('input', floatAddress('pumps', index, name), value[name]))
    }
    pumpDiscrete.push(status.pump1Running, plant.faults.pump1Trip, status.pump2Running, plant.faults.pump2Trip)

    for (const [name, sourceName] of [
      ['processFlow', 'totalFlow'],
      ['ambientTemperature', 'ambientTemperature'],
      ['conductivity', 'conductivity'],
      ['coolingValvePosition', 'coolingValvePosition']
    ]) {
      utility.push(floatWrites('input', floatAddress('utility', index, name), value[sourceName]))
    }
    utilityDiscrete.push(status.temperatureHigh, status.flowLow)
    utilityCoils.push(plant.coolingValvePosition > 5)
  }

  tank.push(bitFieldWrite('discrete', tankDiscrete), bitFieldWrite('coils', tankCoils))
  pumps.push(bitFieldWrite('discrete', pumpDiscrete))
  utility.push(bitFieldWrite('discrete', utilityDiscrete), bitFieldWrite('coils', utilityCoils))
  return { tank, pumps, utility }
}

function dashboardState (state) {
  const source = state || initialState()
  const selected = selectedIndex(source)
  const plant = source.plants[selected]
  const published = plantMeasurements(plant)
  const status = plantStatus(plant)
  return {
    ...published,
    selectedInstance: selected,
    instanceCount: source.plants.length,
    speed: source.speed,
    plants: source.plants.map(item => {
      const values = plantMeasurements(item)
      return {
        ...values,
        controls: { ...item.controls },
        faults: { ...item.faults },
        status: plantStatus(item)
      }
    }),
    controls: {
      ...plant.controls,
      selectedInstance: selected,
      speed: source.speed
    },
    faults: { ...plant.faults },
    status
  }
}

module.exports = {
  DEFAULT_INSTANCE_COUNT,
  MAX_INSTANCE_COUNT,
  INSTANCE_COUNT,
  REGISTER_MAP,
  DEFAULT_CONTROLS,
  DEFAULT_FAULTS,
  parseInstanceCount,
  initialState,
  step,
  applyControl,
  measurements,
  encodeFloat32,
  messagesForSlaves,
  dashboardState,
  floatAddress,
  discreteAddress,
  coilAddress
}
