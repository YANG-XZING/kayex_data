import test from 'node:test'
import assert from 'node:assert/strict'
import { areaOf } from './areas.js'

const areas = [
 { from: '172.30.3.61', to: '172.30.3.65', area: 'A' },
 { from: '172.30.3.71', to: '172.30.3.130', area: 'B' },
]

test('same subnet uses numeric inclusive ranges', () => {
 let a = 0, b = 0
 for (let n = 1; n <= 254; n++) {
  const want = n >= 61 && n <= 65 ? 'A' : n >= 71 && n <= 130 ? 'B' : '其他'
  const got = areaOf(`172.30.3.${n}`, areas)
  assert.equal(got, want)
  if (got === 'A') a++
  if (got === 'B') b++
 }
 assert.equal(a, 5)
 assert.equal(b, 60)
})

test('invalid addresses and obsolete rules do not misclassify devices', () => {
 for (const ip of ['172.30.3.256', '172.30.4.61', '172.30.3.061', '', null]) assert.equal(areaOf(ip, areas), '其他')
 assert.equal(areaOf('172.30.3.61', [{ prefix: '172.30.3.', area: 'A' }]), '其他')
 assert.equal(areaOf('172.30.3.61', [{ from: '172.30.3.65', to: '172.30.3.61', area: 'A' }]), '其他')
})
