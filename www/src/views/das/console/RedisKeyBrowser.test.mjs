// Run from www: node --test src/views/das/console/RedisKeyBrowser.test.mjs
import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { computed, reactive, ref, watch } from 'vue'
const source = readFileSync(new URL('./RedisKeyBrowser.vue', import.meta.url), 'utf8')
const script = source.match(/<script setup>([\s\S]*?)<\/script>/)[1].replace(/^import .*$/gm, '')
function setup(scanApi, valueApi = async () => ({ data: {} })) {
  const props = reactive({ instanceId: 'redis-a' })
  const create = new Function('computed', 'ref', 'watch', 'onBeforeUnmount', 'defineProps', 'ScanRedisKeysApi', 'GetRedisValueApi', 'defineExpose', `${script}\nreturn { db, pattern, keys, selected, detail, scanning, reading, scanCursor, scanMore, scan, readValue, ttl, reset }`)
  return { ...create(computed, ref, watch, () => {}, () => props, scanApi, valueApi, () => {}), props }
}
const deferred = () => { let resolve; const promise = new Promise(r => { resolve = r }); return { promise, resolve } }
test('SCAN preserves large decimal cursors and continues an empty page with the original pattern', async () => {
  const calls = []
  const s = setup(async request => { calls.push(request); return { data: { keys: [], next_cursor: calls.length === 1 ? '18446744073709551615' : '0', has_more: calls.length === 1 } } })
  s.pattern.value = 'prefix:*'; await s.scan(false)
  assert.equal(s.scanMore.value, true); assert.deepEqual(s.keys.value, [])
  s.pattern.value = 'new:*'; await s.scan(true)
  assert.equal(calls[1].cursor, '18446744073709551615'); assert.equal(calls[1].pattern, 'prefix:*'); assert.equal(s.scanMore.value, false)
})
test('DB and instance changes clear state and discard late scan/value responses', async () => {
  const a = deferred(), b = deferred(); const s = setup(() => a.promise, () => b.promise)
  const scan = s.scan(false); s.db.value = 1
  a.resolve({ data: { keys: [{ key: 'stale', key_base64: 'cw==' }], next_cursor: '5' } }); await scan
  assert.deepEqual(s.keys.value, []); assert.equal(s.scanning.value, false)
  s.selected.value = { key: 'old', key_base64: 'b2xk' }; const read = s.readValue(false)
  s.props.instanceId = 'redis-b'; b.resolve({ data: { key: 'old', type: 'string' } }); await read
  assert.equal(s.db.value, 0); assert.equal(s.detail.value, null); assert.equal(s.selected.value, null); assert.equal(s.reading.value, false)
})
test('binary and empty key IDs are sent unchanged; value pages replace rows and preserve cursor/offset', async () => {
  const calls = []; const s = setup(async () => ({data: {}}), async request => { calls.push(request); return { data: { type: 'hash', rows: [{ field: String(calls.length), value: { encoding: 'base64', data: '/w==' } }], next_cursor: '18446744073709551614', next_offset: 100, has_more: true } } })
  s.selected.value = { key: '�', key_base64: '/wA=' }; await s.readValue(false); await s.readValue(true)
  assert.equal(calls[1].key_base64, '/wA='); assert.equal(calls[1].cursor, '18446744073709551614'); assert.equal(calls[1].offset, 100)
  assert.equal(s.detail.value.rows.length, 1); assert.equal(s.detail.value.rows[0].field, '2')
  s.selected.value = { key: '', key_base64: '' }; await s.readValue(false); assert.equal(calls[2].key_base64, '')
  assert.equal(s.ttl(-1), '无过期'); assert.equal(s.ttl(-2), '已不存在')
})
test('failed next scan retains cursor for retry; invalid DB never sends a request', async () => {
  let calls = 0; const s = setup(async () => { calls++; if (calls > 1) throw Error('connection'); return { data: { keys: [{key:'a',key_base64:'YQ=='}], next_cursor:'123',has_more:true } } })
  await s.scan(false); await s.scan(true)
  assert.equal(s.scanCursor.value, '123'); assert.equal(s.keys.value[0].key, 'a'); assert.equal(s.scanning.value, false)
  s.db.value = null; await s.scan(false); assert.equal(calls, 2)
})
test('SCAN COUNT is a hint: every returned entry remains visible, including duplicates', async () => {
  const keys = Array.from({ length: 125 }, (_, i) => ({ key: String(i), key_base64: String(i) }))
  keys.push(keys[0])
  const s = setup(async () => ({ data: { keys, next_cursor: '0', has_more: false } }))
  await s.scan(false)
  assert.equal(s.keys.value.length, 126)
})
