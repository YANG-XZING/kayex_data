// 全站共享状态：登录用户、炉台目录、点位列表和点位信息、当前炉台及其实时值。
import { reactive, computed } from 'vue'

const FURNACES_INTERVAL = 5000
const CLOCK_INTERVAL = 60000
const COLUMNS_INTERVAL = 5 * 60000
const RECONNECT_MIN = 1000
const RECONNECT_MAX = 10000

// 读 save() 存的值（JSON），没有或读不了时为 fallback
export function load(key, fallback) {
  try {
    const v = localStorage.getItem(key)
    return v == null ? fallback : JSON.parse(v)
  } catch { return fallback }
}
export function save(key, value) {
  try { localStorage.setItem(key, JSON.stringify(value)) } catch { /* 隐私模式等情况下忽略 */ }
}

export const state = reactive({
  furnaces: [],
  furnacesError: '',
  checkedAt: 0,
  tdOffsetMs: 0,
  clock: null,          // /api/clock 的 reference
  columns: [],          // [{name, type, numeric}]
  current: load('kx.current', ''), // 当前炉台子表名
  wsConnected: false,
  // 当前炉台的实时值
  live: { table: '', ts: 0, values: {}, changedAt: {} },
  serverNow: 0,         // 服务器推送的 TD 时钟当前时间，用于计算数据新鲜度
  serverDelta: 0,       // TD 时钟 - 浏览器时钟（毫秒），浏览器时钟也不一定准
  // 点位信息（kx_opc.opc_point_info）：点位 -> { tag, name, unit }
  points: {},
  pointTable: '',
  me: null,             // 登录用户 { user, role, auth, hmi_all }；null 为未登录
  settings: { history_max_range_ms: 2 * 3600 * 1000, history_max_tags: 10, table_max_range_ms: 31 * 86400 * 1000,
    hmi_max_range_ms: 31 * 86400 * 1000, alarm_max_range_ms: 31 * 86400 * 1000, skew_limit_s: 1800 },
  // 当前还没恢复的告警（后端已记录的，带防抖）
  alarms: [],
  alarmsReady: false,
})

// 客户账户：只看上位机数据（客户可见的点位，后端已过滤）和上位机记录
export const isCustomer = computed(() => state.me?.role === 'customer')

export const byTable = computed(() => {
  const m = new Map()
  for (const f of state.furnaces) m.set(f.table, f)
  return m
})
export const currentFurnace = computed(() => byTable.value.get(state.current) || null)
// 可以画曲线的列：数值和开关量
export const trendColumns = computed(() => state.columns.filter((c) => c.numeric || c.type === 'BOOL').map((c) => c.name))
// 有告警的炉台数（一台炉台同时两种告警算一台）
export const alarmCount = computed(() => new Set(state.alarms.map((a) => a.device_ip)).size)
export const alarmsByIp = computed(() => {
  const m = new Map()
  for (const a of state.alarms) {
    if (!m.has(a.device_ip)) m.set(a.device_ip, [])
    m.get(a.device_ip).push(a)
  }
  return m
})
// 一台炉台已记录（防抖后）的告警：页面上的红 / 黄标记统一用它，和告警卡片、侧栏数字的口径一致。
// 时间不一致连续 3 轮（约 15 秒）超限才算，偶尔一次波动不变色
export function alarmFlags(ip) {
  const list = alarmsByIp.value.get(ip) || []
  return {
    list,
    stop: list.some((a) => a.alarm_type === 'heartbeat_stop'),
    skew: list.some((a) => a.alarm_type === 'time_skew'),
  }
}
// 炉台小圆点的样式：底色按实时状态（在线 / 心跳停止 / 无数据），已记录的时间不一致告警加黄圈
export function dotClass(f) {
  return [f.status, alarmFlags(f.device_ip).skew ? 'skew' : '']
}
// 超过一半炉台同时心跳停止，多半是采集程序或数据库的问题，而不是炉台本身
export const massOutage = computed(() => {
  const withData = state.furnaces.filter((f) => f.status !== 'no_data').length
  const stopped = state.furnaces.filter((f) => f.status === 'stopped').length
  return withData >= 4 && stopped * 2 > withData
})

export function selectFurnace(table) {
  if (table === state.current) return
  state.current = table
  save('kx.current', table)
  state.live = { table, ts: 0, values: {}, changedAt: {} }
  subscribe()
}

// 登录过期（接口返回 401）时调用，由 main.js 设为跳到登录页
let onUnauthorized = () => {}
export function setUnauthorizedHandler(fn) { onUnauthorized = fn }

async function request(url, opts) {
  const r = await fetch(url, { cache: 'no-store', ...opts })
  const body = await r.json().catch(() => ({}))
  if (r.status === 401 && state.me) {
    state.me = null
    onUnauthorized()
  }
  if (!r.ok) throw new Error(body.error || r.statusText)
  return body
}
export function getJSON(url, opts) { return request(url, opts) }
export function postJSON(url, data) {
  return request(url, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(data) })
}

// ---------- 登录 ----------
// 读取当前登录用户（未登录为 null）；没配置账户时后端直接返回内部账户
export async function loadMe() {
  try { state.me = await getJSON('/api/me') } catch { state.me = null }
  return state.me
}
export async function login(user, password) {
  state.me = await postJSON('/api/login', { user, password })
  return state.me
}
// 客户账户能打开的页面；内部账户全部页面都能打开
const CUSTOMER_PAGES = ['/hmi/data', '/hmi/export', '/hmi/records/record', '/hmi/records/alarm']
export function canVisit(me, path) { return me?.role !== 'customer' || CUSTOMER_PAGES.includes(path) }
export function homeOf(me) { return me?.role === 'customer' ? CUSTOMER_PAGES[0] : '/curves/live' }
// 退出后整页重新加载，定时器、实时连接和缓存的数据都清掉
export async function logout() {
  try { await postJSON('/api/logout', {}) } catch { /* 忽略 */ }
  location.hash = '#/login'
  location.reload()
}

// ---------- 点位信息 ----------
export async function loadPoints() {
  try {
    const d = await getJSON('/api/points')
    state.points = d.points
    state.pointTable = d.table
  } catch { /* 下次再试 */ }
}

export function fieldOf(col) { return state.points[col] || {} }
export function labelOf(col) { return fieldOf(col).name || col }

async function pollFurnaces() {
  try {
    const s = await getJSON('/api/furnaces')
    state.furnaces = s.furnaces
    state.furnacesError = s.error || ''
    state.checkedAt = Date.parse(s.checked_at) || 0
    state.tdOffsetMs = s.td_offset_ms
  } catch (e) {
    state.furnacesError = '无法连接网站后端：' + e.message
  }
}
async function pollAlarms() {
  if (isCustomer.value) return // 客户账户没有通讯预警
  try {
    const d = await getJSON('/api/alarms/active')
    state.alarms = d.alarms
    state.alarmsReady = d.ready
  } catch { /* 下次再试 */ }
}

async function pollClock() {
  try {
    const d = await getJSON('/api/clock')
    state.clock = d.reference
    state.serverDelta = d.now + d.td_offset_ms - Date.now()
  } catch { /* 下次再试 */ }
}
// 服务器时钟（TD 时钟）的当前时间：数据和告警的时间都用它
export function serverNow() { return Date.now() + state.serverDelta }
async function pollColumns() {
  try { state.columns = await getJSON('/api/columns') } catch { /* 下次再试 */ }
}

// ---------- WebSocket ----------
let ws = null
let retry = RECONNECT_MIN

function subscribe() {
  if (ws && ws.readyState === WebSocket.OPEN) {
    ws.send(JSON.stringify({ op: 'sub', tables: state.current ? [state.current] : [] }))
  }
}

function connect() {
  const proto = location.protocol === 'https:' ? 'wss' : 'ws'
  ws = new WebSocket(`${proto}://${location.host}/ws`)
  ws.onopen = () => {
    state.wsConnected = true
    retry = RECONNECT_MIN
    subscribe()
  }
  ws.onclose = () => {
    state.wsConnected = false
    setTimeout(connect, retry)
    retry = Math.min(retry * 2, RECONNECT_MAX)
  }
  ws.onmessage = (e) => {
    const m = JSON.parse(e.data)
    if (m.t === 'now') {
      state.serverNow = m.now
      state.serverDelta = m.now - Date.now()
    }
    else if (m.t === 'row') onRow(m)
  }
}

function onRow(m) {
  if (m.table !== state.current) return
  const live = state.live
  if (m.full || live.table !== m.table) {
    state.live = { table: m.table, ts: m.ts, values: m.v, changedAt: {} }
  } else {
    const now = Date.now()
    Object.assign(live.values, m.v)
    for (const k in m.v) live.changedAt[k] = now
    live.ts = m.ts
  }
}

let started = false
// 登录后开始轮询和实时连接
export function start() {
  if (started) return
  started = true
  pollFurnaces(); pollAlarms(); pollClock(); pollColumns(); loadPoints()
  getJSON('/api/settings').then((s) => { state.settings = s }).catch(() => {})
  setInterval(pollFurnaces, FURNACES_INTERVAL)
  setInterval(pollAlarms, FURNACES_INTERVAL)
  setInterval(pollClock, CLOCK_INTERVAL)
  setInterval(pollColumns, COLUMNS_INTERVAL)
  setInterval(loadPoints, COLUMNS_INTERVAL) // 别人改了点位信息，几分钟内同步过来
  connect()
}

// ---------- 格式化 ----------
// 数值最多 3 位小数并去掉末尾的 0
export function fmtValue(v) {
  if (v === null || v === undefined || v === '') return '—'
  if (typeof v === 'boolean') return v ? '开' : '关'
  if (typeof v === 'number') {
    return Number.isInteger(v) ? String(v) : String(+v.toFixed(3))
  }
  return String(v)
}
export function fmtField(col, v) { return fmtValue(v) }
// datetime-local 输入框的值 <-> 毫秒
export function toInput(ms) {
  const d = new Date(ms)
  const p = (n) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}T${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`
}
export function fromInput(s) { return s ? new Date(s).getTime() : NaN }
// “最近 N”快捷按钮：不超过 maxMs 的几档（历史数据表格、上位机记录共用）
const QUICK_RANGES = [
  ['10 分钟', 10 * 60000], ['1 小时', 3600000], ['1 天', 86400000], ['7 天', 7 * 86400000], ['31 天', 31 * 86400000],
]
export function quickRanges(maxMs) {
  return QUICK_RANGES.filter(([, ms]) => ms <= maxMs).map(([label, ms]) => ({ label, ms }))
}
export function fmtTime(ms) {
  if (!ms) return '—'
  const d = new Date(ms)
  const p = (n) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`
}
export function fmtDuration(s) {
  s = Math.abs(s)
  if (s < 60) return `${Math.round(s)} 秒`
  // 一位小数，整数时不显示 .0
  if (s < 3600) return `${+(s / 60).toFixed(1)} 分`
  if (s < 86400) return `${+(s / 3600).toFixed(1)} 小时`
  return `${+(s / 86400).toFixed(1)} 天`
}
export function fmtSkew(s) {
  if (s === null || s === undefined) return '—'
  if (Math.abs(s) < 1) return '一致'
  return (s > 0 ? '快 ' : '慢 ') + fmtDuration(s)
}
export const ALARM_TEXT = { heartbeat_stop: '心跳停止', time_skew: '时间不一致', heartbeat_jump: '心跳跳变' }
// 告警的值：心跳停止为停了多久，时间不一致为快 / 慢多少
export function fmtAlarmValue(type, s) {
  if (type === 'heartbeat_jump') return (s > 0 ? '向前跳 ' : '向后跳 ') + fmtDuration(s)
  return type === 'time_skew' ? fmtSkew(s) : '停 ' + fmtDuration(s)
}
export const STATUS_TEXT = { online: '在线', stopped: '心跳停止', no_data: '无数据' }
export const STATUS_CLASS = { online: 'ok', stopped: 'danger', no_data: 'mute' }
