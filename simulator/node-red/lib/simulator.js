'use strict'

const TANK_COUNT = 10
const TANK_FLOAT_STRIDE = 20
const TANK_DISCRETE_STRIDE = 3
const TANK_COIL_STRIDE = 2

const REGISTER_MAP = Object.freeze({
  tank: Object.freeze({
    port: 1502,
    unitId: 1,
    count: TANK_COUNT,
    floatStride: TANK_FLOAT_STRIDE,
    discreteStride: TANK_DISCRETE_STRIDE,
    coilStride: TANK_COIL_STRIDE,
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
  speed: 1,
  selectedTank: 0,
  coolingCommand: 40,
  pump1Command: true,
  pump2Command: false
})

const DEFAULT_PLANT_FAULTS = Object.freeze({
  pump1Trip: false,
  pump2Trip: false,
  sensorFreeze: false
})

const DEFAULT_TANK_FAULTS = Object.freeze({
  inletStuck: false,
  outletStuck: false,
  highTemperature: false,
  sensorBad: false
})

const TANK_COMMAND_TOPICS = new Set(['inletCommand', 'outletCommand'])
const TANK_FAULT_TOPICS = new Set(Object.keys(DEFAULT_TANK_FAULTS))
const BOOLEAN_PLANT_CONTROLS = new Set(['automatic', 'pump1Command', 'pump2Command'])

function clamp (value, min, max) {
  return Math.min(max, Math.max(min, value))
}

function selectedTankIndex (state) {
  return clamp(Math.round(Number(state.controls.selectedTank) || 0), 0, TANK_COUNT - 1)
}

function selectedTank (state) {
  return state.tanks[selectedTankIndex(state)]
}

function initialTank (index) {
  return {
    id: index + 1,
    seed: (0x5ca1ab1e ^ Math.imul(index + 1, 0x9e3779b9)) >>> 0,
    level: 40 + index * 3,
    temperature: 22 + index * 0.4,
    pressure: 1.8 + index * 0.05,
    inletPosition: 70,
    outletPosition: 45,
    inletCommand: 70,
    outletCommand: 45,
    faults: { ...DEFAULT_TANK_FAULTS }
  }
}

function initialState () {
  return {
    tick: 0,
    seed: 0x5ca1ab1e,
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
    tanks: Array.from({ length: TANK_COUNT }, (_, index) => initialTank(index)),
    controls: { ...DEFAULT_CONTROLS },
    faults: { ...DEFAULT_PLANT_FAULTS },
    frozenMeasurements: null
  }
}

function nextNoise (rng, amplitude) {
  rng.seed = (Math.imul(rng.seed, 1664525) + 1013904223) >>> 0
  return ((rng.seed / 0xffffffff) * 2 - 1) * amplitude
}

function approach (value, target, amount) {
  return value + (target - value) * clamp(amount, 0, 1)
}

function maxTankValue (tanks, field) {
  return tanks.reduce((highest, tank) => Math.max(highest, tank[field]), tanks[0][field])
}

function meanTankValue (tanks, field) {
  return tanks.reduce((sum, tank) => sum + tank[field], 0) / tanks.length
}

function tankFloatAddress (tankIndex, field) {
  return tankIndex * TANK_FLOAT_STRIDE + REGISTER_MAP.tank.floats[field]
}

function tankDiscreteAddress (tankIndex, field) {
  return tankIndex * TANK_DISCRETE_STRIDE + REGISTER_MAP.tank.discrete[field]
}

function tankCoilAddress (tankIndex, field) {
  return tankIndex * TANK_COIL_STRIDE + REGISTER_MAP.tank.coils[field]
}

function step (previous, elapsedSeconds = 1) {
  const state = previous || initialState()
  const dt = clamp(Number(elapsedSeconds) || 1, 0.05, 10) *
    clamp(Number(state.controls.speed) || 1, 0.1, 10)

  if (state.controls.automatic) {
    for (const tank of state.tanks) {
      const levelError = 60 - tank.level
      tank.inletCommand = clamp(58 + levelError * 1.8, 5, 100)
      tank.outletCommand = clamp(42 - levelError * 0.7, 5, 90)
    }
    const maxLevel = maxTankValue(state.tanks, 'level')
    const maxTemperature = maxTankValue(state.tanks, 'temperature')
    state.controls.pump1Command = maxLevel > 18
    state.controls.pump2Command = maxLevel > 72
    state.controls.coolingCommand = clamp(35 + (maxTemperature - 25) * 5, 10, 100)
  }

  for (const tank of state.tanks) {
    if (!tank.faults.inletStuck) {
      tank.inletPosition = approach(tank.inletPosition, tank.inletCommand, 0.18 * dt)
    }
    if (!tank.faults.outletStuck) {
      tank.outletPosition = approach(tank.outletPosition, tank.outletCommand, 0.18 * dt)
    }
  }
  state.coolingValvePosition = approach(
    state.coolingValvePosition,
    state.controls.coolingCommand,
    0.15 * dt
  )

  const anyProduct = state.tanks.some(tank => tank.level > 3)
  const pump1Running = Boolean(state.controls.pump1Command && !state.faults.pump1Trip && anyProduct)
  const pump2Running = Boolean(state.controls.pump2Command && !state.faults.pump2Trip && anyProduct)
  state.pump1Speed = approach(state.pump1Speed, pump1Running ? 72 : 0, 0.25 * dt)
  state.pump2Speed = approach(state.pump2Speed, pump2Running ? 65 : 0, 0.25 * dt)

  const pumpedFlow = state.pump1Speed * 0.36 + state.pump2Speed * 0.32
  const meanOutlet = meanTankValue(state.tanks, 'outletPosition')
  state.totalFlow = Math.max(0, meanOutlet * 0.16 + pumpedFlow + nextNoise(state, 0.25))

  const cooling = state.coolingValvePosition * 0.018
  for (const tank of state.tanks) {
    const inletFlow = tank.inletPosition * 0.42
    const outflow = tank.outletPosition * 0.16 + pumpedFlow
    tank.level = clamp(tank.level + (inletFlow - outflow) * 0.018 * dt, 0, 100)

    const temperatureTarget = tank.faults.highTemperature
      ? 96
      : state.ambientTemperature + 3 + (pump1Running ? 2.5 : 0) + (pump2Running ? 2 : 0)
    tank.temperature = clamp(
      tank.temperature + (temperatureTarget - tank.temperature) * 0.012 * dt - cooling * 0.025 * dt,
      -20,
      130
    )
    tank.pressure = clamp(
      0.8 + tank.level * 0.027 + pumpedFlow * 0.035 + nextNoise(tank, 0.015),
      0,
      10
    )
  }

  state.dischargePressure = clamp(0.6 + pumpedFlow * 0.075 + nextNoise(state, 0.02), 0, 12)
  state.pump1Current = Math.max(0, state.pump1Speed * 0.25 + nextNoise(state, 0.08))
  state.pump2Current = Math.max(0, state.pump2Speed * 0.27 + nextNoise(state, 0.08))
  state.vibration = Math.max(0, 0.25 + (state.pump1Speed + state.pump2Speed) * 0.009 + nextNoise(state, 0.03))
  state.ambientTemperature = clamp(21 + Math.sin(state.tick / 90) * 2, 15, 30)
  state.conductivity = clamp(470 + meanTankValue(state.tanks, 'level') * 0.25 + nextNoise(state, 1.2), 0, 2000)
  state.tick += 1
  return state
}

function applyControl (state, topic, payload) {
  const target = state || initialState()
  const booleanValue = payload === true || payload === 'true' || payload === 1 || payload === '1'
  const numericValue = Number(payload)
  const plantControls = {
    automatic: value => { target.controls.automatic = value },
    speed: value => { target.controls.speed = clamp(value, 0.1, 10) },
    selectedTank: value => {
      target.controls.selectedTank = clamp(Math.round(value), 0, TANK_COUNT - 1)
    },
    coolingCommand: value => { target.controls.coolingCommand = clamp(value, 0, 100) },
    pump1Command: value => { target.controls.pump1Command = value },
    pump2Command: value => { target.controls.pump2Command = value }
  }

  if (Object.hasOwn(plantControls, topic)) {
    if (BOOLEAN_PLANT_CONTROLS.has(topic)) plantControls[topic](booleanValue)
    else if (Number.isFinite(numericValue)) plantControls[topic](numericValue)
  } else if (TANK_COMMAND_TOPICS.has(topic) && Number.isFinite(numericValue)) {
    selectedTank(target)[topic] = clamp(numericValue, 0, 100)
  } else if (Object.hasOwn(DEFAULT_PLANT_FAULTS, topic)) {
    target.faults[topic] = booleanValue
    if (topic === 'sensorFreeze') {
      target.frozenMeasurements = booleanValue ? measurements(target, false) : null
    }
  } else if (TANK_FAULT_TOPICS.has(topic)) {
    selectedTank(target).faults[topic] = booleanValue
  } else if (topic === 'resetFaults') {
    target.faults = { ...DEFAULT_PLANT_FAULTS }
    target.frozenMeasurements = null
    for (const tank of target.tanks) {
      tank.faults = { ...DEFAULT_TANK_FAULTS }
    }
  }
  return target
}

function tankMeasurements (tank, applySensorFaults = true) {
  const values = {
    id: tank.id,
    level: tank.level,
    temperature: tank.temperature,
    pressure: tank.pressure,
    inletPosition: tank.inletPosition,
    outletPosition: tank.outletPosition,
    sensorBad: tank.faults.sensorBad
  }
  if (applySensorFaults && tank.faults.sensorBad) {
    values.level = 999.9
    values.temperature = 999.9
  }
  return values
}

function measurements (state, applySensorFaults = true) {
  if (applySensorFaults && state.faults.sensorFreeze && state.frozenMeasurements) {
    return {
      ...state.frozenMeasurements,
      tanks: state.frozenMeasurements.tanks.map(tank => ({ ...tank, sensorBad: false }))
    }
  }
  return {
    tanks: state.tanks.map(tank => tankMeasurements(tank, applySensorFaults)),
    pump1Speed: state.pump1Speed,
    pump1Current: state.pump1Current,
    dischargePressure: state.dischargePressure,
    pump2Speed: state.pump2Speed,
    pump2Current: state.pump2Current,
    totalFlow: state.totalFlow,
    vibration: state.vibration,
    ambientTemperature: state.ambientTemperature,
    conductivity: state.conductivity,
    coolingValvePosition: state.coolingValvePosition
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
  const value = measurements(state)
  const pump1Running = state.pump1Speed > 5 && !state.faults.pump1Trip
  const pump2Running = state.pump2Speed > 5 && !state.faults.pump2Trip
  const tank = []
  const discreteBits = []
  const coilBits = []
  for (let index = 0; index < TANK_COUNT; index++) {
    const published = value.tanks[index]
    const source = state.tanks[index]
    for (const name of Object.keys(REGISTER_MAP.tank.floats)) {
      tank.push(floatWrites('input', tankFloatAddress(index, name), published[name]))
    }
    discreteBits.push(source.level >= 80, source.level <= 20, published.sensorBad)
    coilBits.push(source.inletPosition > 5, source.outletPosition > 5)
  }
  tank.push(bitFieldWrite('discrete', discreteBits), bitFieldWrite('coils', coilBits))

  const pumps = []
  for (const [name, address] of Object.entries(REGISTER_MAP.pumps.floats)) {
    pumps.push(floatWrites('input', address, value[name]))
  }
  pumps.push(
    bitFieldWrite('discrete', [
      pump1Running,
      state.faults.pump1Trip,
      pump2Running,
      state.faults.pump2Trip
    ])
  )

  const hottest = maxTankValue(state.tanks, 'temperature')
  const utility = []
  for (const [name, address] of Object.entries(REGISTER_MAP.utility.floats)) {
    const sourceName = name === 'processFlow' ? 'totalFlow' : name
    utility.push(floatWrites('input', address, value[sourceName]))
  }
  utility.push(
    bitFieldWrite('discrete', [hottest >= 80, value.totalFlow < 10]),
    bitFieldWrite('coils', [state.coolingValvePosition > 5])
  )
  return { tank, pumps, utility }
}

function dashboardState (state) {
  const value = measurements(state)
  const selected = selectedTankIndex(state)
  const tank = value.tanks[selected]
  const source = state.tanks[selected]
  return {
    ...value,
    ...tank,
    selectedTank: selected,
    tanks: value.tanks.map((published, index) => ({
      ...published,
      inletCommand: state.tanks[index].inletCommand,
      outletCommand: state.tanks[index].outletCommand,
      faults: { ...state.tanks[index].faults },
      status: {
        levelHigh: state.tanks[index].level >= 80,
        levelLow: state.tanks[index].level <= 20,
        temperatureHigh: state.tanks[index].temperature >= 80
      }
    })),
    controls: {
      ...state.controls,
      selectedTank: selected,
      inletCommand: source.inletCommand,
      outletCommand: source.outletCommand
    },
    faults: {
      ...state.faults,
      ...source.faults
    },
    status: {
      levelHigh: state.tanks.some(item => item.level >= 80),
      levelLow: state.tanks.some(item => item.level <= 20),
      pump1Running: state.pump1Speed > 5 && !state.faults.pump1Trip,
      pump2Running: state.pump2Speed > 5 && !state.faults.pump2Trip,
      temperatureHigh: maxTankValue(state.tanks, 'temperature') >= 80,
      flowLow: state.totalFlow < 10
    }
  }
}

module.exports = {
  TANK_COUNT,
  TANK_FLOAT_STRIDE,
  TANK_DISCRETE_STRIDE,
  TANK_COIL_STRIDE,
  REGISTER_MAP,
  DEFAULT_CONTROLS,
  DEFAULT_PLANT_FAULTS,
  DEFAULT_TANK_FAULTS,
  initialState,
  step,
  applyControl,
  measurements,
  encodeFloat32,
  messagesForSlaves,
  dashboardState,
  tankFloatAddress,
  tankDiscreteAddress,
  tankCoilAddress
}
