'use strict'

const test = require('node:test')
const assert = require('node:assert/strict')
const simulator = require('../lib/simulator')

function isValidWriteValue (value) {
  if (Number.isInteger(value)) return true
  return Array.isArray(value) && value.length > 0 && value.every(Number.isInteger)
}

test('encodes float32 as big-endian Modbus words', () => {
  assert.deepEqual(simulator.encodeFloat32(12.5), [0x4148, 0x0000])
  assert.deepEqual(simulator.encodeFloat32(-2.25), [0xc010, 0x0000])
})

test('instance count defaults to 10 and can be overridden', () => {
  assert.equal(simulator.parseInstanceCount(undefined), 10)
  assert.equal(simulator.parseInstanceCount('25'), 25)
  assert.equal(simulator.parseInstanceCount('0'), 10)
  assert.equal(simulator.parseInstanceCount('-3'), 10)
  assert.equal(simulator.parseInstanceCount('1.5'), 10)
  assert.equal(simulator.parseInstanceCount('1000'), simulator.MAX_INSTANCE_COUNT)
  assert.equal(simulator.initialState(4).plants.length, 4)
  assert.equal(
    simulator.INSTANCE_COUNT,
    simulator.parseInstanceCount(process.env.SIMULATOR_INSTANCES)
  )
})

test('each instance is a complete independent plant', () => {
  const state = simulator.initialState()
  assert.equal(state.plants.length, simulator.INSTANCE_COUNT)
  const seeds = new Set(state.plants.map(plant => plant.seed))
  assert.equal(seeds.size, simulator.INSTANCE_COUNT)
  for (const plant of state.plants) {
    assert.equal(plant.level, 55)
    assert.ok(Object.hasOwn(plant.controls, 'pump2Command'))
    assert.ok(Object.hasOwn(plant.faults, 'pump2Trip'))
    assert.ok(Object.hasOwn(plant, 'inletPosition'))
    assert.ok(Object.hasOwn(plant, 'coolingValvePosition'))
  }
})

test('automatic model remains inside physical bounds', () => {
  const state = simulator.initialState()
  for (let index = 0; index < 10000; index++) simulator.step(state, 1)
  for (const plant of state.plants) {
    assert.ok(plant.level >= 0 && plant.level <= 100)
    assert.ok(plant.temperature >= -20 && plant.temperature <= 130)
    assert.ok(plant.pressure >= 0 && plant.pressure <= 10)
    assert.ok(plant.inletPosition >= 0 && plant.inletPosition <= 100)
    assert.ok(plant.outletPosition >= 0 && plant.outletPosition <= 100)
    assert.ok(plant.pump1Speed >= 0 && plant.pump1Speed <= 100)
    assert.ok(plant.pump2Speed >= 0 && plant.pump2Speed <= 100)
  }
})

test('plants keep distinct process values after a long automatic run', () => {
  const state = simulator.initialState()
  for (let index = 0; index < 200; index++) simulator.step(state, 1)
  const signatures = new Set(
    state.plants.map(plant => `${plant.level.toFixed(4)}:${plant.pressure.toFixed(4)}`)
  )
  assert.ok(signatures.size > 1)
})

test('pump trip only stops the selected plant pump', () => {
  const state = simulator.initialState()
  simulator.applyControl(state, 'automatic', false)
  simulator.applyControl(state, 'pump2Command', true)
  simulator.applyControl(state, 'selectedInstance', 3)
  simulator.applyControl(state, 'automatic', false)
  simulator.applyControl(state, 'pump2Command', true)
  simulator.applyControl(state, 'pump2Trip', true)
  for (let index = 0; index < 30; index++) simulator.step(state, 1)
  assert.ok(state.plants[3].pump2Speed < 0.1)
  assert.ok(state.plants[0].pump2Speed > 50)
  assert.equal(state.plants[0].faults.pump2Trip, false)
  simulator.applyControl(state, 'selectedInstance', 3)
  assert.equal(simulator.dashboardState(state).status.pump2Running, false)
})

test('sensor freeze preserves only the selected plant analog measurements', () => {
  const state = simulator.initialState()
  simulator.applyControl(state, 'sensorFreeze', true)
  const frozen = simulator.measurements(state).plants[0]
  for (let index = 0; index < 20; index++) simulator.step(state, 1)
  assert.deepEqual(simulator.measurements(state).plants[0], frozen)
  assert.notEqual(simulator.measurements(state).plants[1].level, frozen.level)
  simulator.applyControl(state, 'sensorFreeze', false)
  assert.notEqual(simulator.measurements(state).plants[0].level, frozen.level)
})

test('valve commands and faults apply only to the selected plant', () => {
  const state = simulator.initialState()
  simulator.applyControl(state, 'automatic', false)
  simulator.applyControl(state, 'selectedInstance', 3)
  simulator.applyControl(state, 'automatic', false)
  simulator.applyControl(state, 'inletCommand', 12)
  simulator.applyControl(state, 'inletStuck', true)
  assert.equal(state.plants[3].controls.inletCommand, 12)
  assert.equal(state.plants[3].faults.inletStuck, true)
  assert.equal(state.plants[0].controls.inletCommand, 70)
  assert.equal(state.plants[0].faults.inletStuck, false)
})

test('register map float ranges do not overlap', () => {
  for (const slave of Object.values(simulator.REGISTER_MAP)) {
    const occupied = new Set()
    for (let index = 0; index < slave.count; index++) {
      for (const address of Object.values(slave.floats)) {
        const start = index * slave.floatStride + address
        assert.equal(occupied.has(start), false)
        assert.equal(occupied.has(start + 1), false)
        occupied.add(start)
        occupied.add(start + 1)
      }
    }
  }
})

test('last instance publishes at the final register block of each PLC', () => {
  const last = simulator.INSTANCE_COUNT - 1
  assert.equal(simulator.floatAddress('tank', 0, 'level'), 0)
  assert.equal(simulator.floatAddress('pumps', 0, 'pump1Speed'), 0)
  assert.equal(simulator.floatAddress('utility', 0, 'processFlow'), 0)
  assert.equal(simulator.floatAddress('tank', last, 'level'), last * 20)
  assert.equal(simulator.floatAddress('pumps', last, 'vibration'), last * 28 + 24)
  assert.equal(simulator.floatAddress('utility', last, 'coolingValvePosition'), last * 16 + 12)
  assert.equal(simulator.discreteAddress('pumps', last, 'pump2Trip'), last * 4 + 3)

  const writes = simulator.messagesForSlaves(simulator.initialState())
  const tankLast = writes.tank.find(
    message => message.payload.register === 'input' &&
      message.payload.address === simulator.floatAddress('tank', last, 'level') / 4
  )
  const pumpLast = writes.pumps.find(
    message => message.payload.register === 'input' &&
      message.payload.address === simulator.floatAddress('pumps', last, 'pump1Speed') / 4
  )
  assert.ok(tankLast)
  assert.ok(pumpLast)
})

test('each slave bitfield covers every instance', () => {
  const writes = simulator.messagesForSlaves(simulator.initialState())
  const count = simulator.INSTANCE_COUNT
  const tankDiscrete = writes.tank.find(message => message.payload.register === 'discrete')
  const pumpDiscrete = writes.pumps.find(message => message.payload.register === 'discrete')
  const utilityCoils = writes.utility.find(message => message.payload.register === 'coils')
  assert.ok(tankDiscrete.payload.value.length * 8 >= count * 3)
  assert.ok(pumpDiscrete.payload.value.length * 8 >= count * 4)
  assert.ok(utilityCoils.payload.value.length * 8 >= count)
})

test('every slave emits valid memory writes', () => {
  const writes = simulator.messagesForSlaves(simulator.initialState())
  assert.equal(
    writes.tank.filter(message => message.payload.register === 'input').length,
    simulator.INSTANCE_COUNT * Object.keys(simulator.REGISTER_MAP.tank.floats).length
  )
  assert.equal(
    writes.pumps.filter(message => message.payload.register === 'input').length,
    simulator.INSTANCE_COUNT * Object.keys(simulator.REGISTER_MAP.pumps.floats).length
  )
  for (const slaveWrites of Object.values(writes)) {
    assert.ok(slaveWrites.length > 0)
    for (const message of slaveWrites) {
      assert.ok(['input', 'discrete', 'coils'].includes(message.payload.register))
      assert.ok(Number.isInteger(message.payload.address))
      assert.ok(isValidWriteValue(message.payload.value))
    }
  }
})
