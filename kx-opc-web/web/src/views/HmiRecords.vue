<script setup>
// 上位机记录：上位机写入 kayex_data 的操作记录、报警记录（侧栏两个子菜单，route.meta.hmi），按时间（最长 31 天）和关键字查询，可导出 CSV。
// 当前炉台（上方选择）或全部炉台；按 ts 升序排，默认每页 10 条，支持快捷选择及自定义条数。表格和导出按上位机时间筛选和显示。
// 内部账户和客户账户共用这个页面。
import { ref, shallowRef, computed, watch, onBeforeUnmount } from 'vue'
import { useRoute } from 'vue-router'
import Pager from '../components/Pager.vue'
import PageSizePicker from '../components/PageSizePicker.vue'
import { state, currentFurnace, getJSON, load as loadSaved, save, fmtDuration, serverNow, quickRanges } from '../store.js'

const pageSize = ref(10)
const HMI_UTC_OFFSET_MS = 8 * 60 * 60 * 1000
const RECORD_COLOR_STYLE = {
  white: { label: '白色', tint: '#ffffff' },
  yellow: { label: '黄色', tint: '#fff8db' },
  red: { label: '红色', tint: '#fce9eb' },
  orange: { label: '橙色', tint: '#fff0e3' },
  green: { label: '绿色', tint: '#e9f6ec' },
  blue: { label: '蓝色', tint: '#eaf3fd' },
  purple: { label: '紫色', tint: '#f3ecfa' },
  gray: { label: '灰色', tint: '#f0f2f5' },
  black: { label: '黑色', tint: '#e8ebf0' },
}

const route = useRoute()
const kind = computed(() => route.meta.hmi) // record：操作记录；alarm：报警记录
const canViewAll = computed(() => state.me?.hmi_all === true)
const savedScope = ref(loadSaved('kx.hmiScope', 'one'))
const scope = computed({
  get: () => canViewAll.value && savedScope.value === 'all' ? 'all' : 'one',
  set: (value) => {
    savedScope.value = value
    save('kx.hmiScope', value)
  },
})

const maxRange = computed(() => state.settings.hmi_max_range_ms)
const quickList = computed(() => quickRanges(maxRange.value))
const end = ref(toHmiInput(serverNow()))
const start = ref(toHmiInput(serverNow() - 86400000))
const keyword = ref('')
const color = ref('')

function toHmiInput(ms) {
  return new Date(ms + HMI_UTC_OFFSET_MS).toISOString().slice(0, 19)
}

function fromHmiInput(value) {
  return value ? Date.parse(value + '+08:00') : NaN
}

function quick(ms) {
  const e = Math.floor(serverNow() / 1000) * 1000
  end.value = toHmiInput(e)
  start.value = toHmiInput(e - ms)
  query()
}

// 校验并生成查询参数
const scopeParams = computed(() => {
  const from = fromHmiInput(start.value)
  const to = fromHmiInput(end.value)
  let error = ''
  if (scope.value === 'one' && !currentFurnace.value) error = canViewAll.value ? '请先在上方选择炉台，或者选“全部炉台”' : '请先在上方选择炉台'
  else if (!(from < to)) error = '开始时间必须早于结束时间'
  else if (to - from > maxRange.value) error = `时间范围不能超过 ${fmtDuration(maxRange.value / 1000)}`
  const qs = new URLSearchParams({ kind: kind.value, from, to })
  if (scope.value === 'one' && currentFurnace.value) qs.set('ip', currentFurnace.value.device_ip)
  return { error, qs: qs.toString() }
})
const params = computed(() => {
  const { error, qs: base } = scopeParams.value
  const qs = new URLSearchParams(base)
  if (keyword.value.trim()) qs.set('q', keyword.value.trim())
  if (kind.value === 'record' && color.value) qs.set('color', color.value)
  return { error, qs: qs.toString() }
})

const availableColors = shallowRef([])
const colorState = ref('')
const colorScope = computed(() => kind.value === 'record' && !scopeParams.value.error ? scopeParams.value.qs : '')
watch(colorScope, (qs, _, onCleanup) => {
  color.value = ''
  availableColors.value = []
  colorState.value = qs ? '读取颜色中…' : ''
  if (!qs) return
  const controller = new AbortController()
  const timer = setTimeout(async () => {
    try {
      const colors = await getJSON('/api/hmi/records/colors?' + qs, { signal: controller.signal })
      if (controller.signal.aborted) return
      availableColors.value = colors
      colorState.value = colors.length ? '' : '该范围内无颜色'
    } catch {
      if (!controller.signal.aborted) colorState.value = '颜色读取失败'
    }
  }, 200)
  onCleanup(() => {
    clearTimeout(timer)
    controller.abort()
  })
}, { immediate: true, flush: 'sync' })

function colorLabel(name) {
  return Object.hasOwn(RECORD_COLOR_STYLE, name) ? RECORD_COLOR_STYLE[name].label : name
}

const error = ref('')
const loading = ref(false)
const result = shallowRef(null) // 查询条件 { qs, title }
const page = ref(1)
const data = shallowRef(null) // 当前页 { columns, rows, total }
const colorIndex = computed(() => data.value?.columns.findIndex((c) => c.key === 'color') ?? -1)
let gen = 0 // 翻得快时只用最后一次请求的结果

function rowTint(value) {
  if (!value) return null
  const name = value.toLowerCase()
  if (Object.hasOwn(RECORD_COLOR_STYLE, name)) return RECORD_COLOR_STYLE[name].tint
  if (name === 'grey') return RECORD_COLOR_STYLE.gray.tint
  const named = Object.values(RECORD_COLOR_STYLE).find((color) => color.label === name || color.label === name + '色')
  if (named) return named.tint
  let css = name.replace(/^0x([\da-f]{6})$/, '#$1')
  if (/^-\d+$/.test(name) && Number(name) >= -2147483648) {
    // .NET 的有符号 ARGB，低 24 位为 RGB。
    css = '#' + (Number(name) & 0xffffff).toString(16).padStart(6, '0')
  }
  return CSS.supports('color', css) ? `color-mix(in srgb, ${css} 12%, white)` : null
}

let controller = null
function cancel() { gen++; controller?.abort(); controller = null }
async function load(target = 1) {
  cancel()
  if (!result.value) { loading.value = false; return }
  const g = gen
  const size = pageSize.value
  controller = new AbortController()
  loading.value = true
  try {
    const d = await getJSON(`/api/hmi/records?${result.value.qs}&offset=${(target - 1) * size}&limit=${size}`, { signal: controller.signal })
    if (g !== gen) return
    const pages = Math.max(1, Math.ceil(d.total / size))
    if (target > pages) return load(pages)
    data.value = d
    page.value = target
    error.value = ''
  } catch (e) {
    if (g === gen) error.value = '查询失败：' + e.message
  } finally {
    if (g === gen) loading.value = false
  }
}
watch(pageSize, () => {
  page.value = 1
  data.value = null
  if (result.value) load(1)
})
onBeforeUnmount(cancel)

function query() {
  const p = params.value
  error.value = p.error
  if (p.error) return
  result.value = { qs: p.qs, title: `${route.meta.title} · ${scope.value === 'all' ? '全部炉台' : currentFurnace.value.device_id}` }
  data.value = null
  page.value = 1
  load(1)
}
function exportCSV() {
  const p = params.value
  error.value = p.error
  if (!p.error) location.href = '/api/hmi/records/export?' + p.qs
}

// 换种类、范围、炉台后旧结果作废
// sync：条件一变立即作废，不会误伤紧接着发起的新查询
watch([kind, scope, () => state.current], ([nextKind], [previousKind]) => {
  cancel()
  result.value = null
  data.value = null
  loading.value = false
  page.value = 1
  if (nextKind !== previousKind) pageSize.value = 10
}, { flush: 'sync' })
</script>

<template>
  <div class="card q">
    <div class="row">
      <span class="k">炉台</span>
      <div class="seg">
        <button :class="{ on: scope === 'one' }" @click="scope = 'one'">当前炉台{{ currentFurnace ? `（${currentFurnace.device_id}）` : '' }}</button>
        <button v-if="canViewAll" :class="{ on: scope === 'all' }" @click="scope = 'all'">全部炉台</button>
      </div>
    </div>
    <div class="row">
      <span class="k">时间</span>
      <input v-model="start" class="input t" type="datetime-local" step="1" />
      <span class="muted">至</span>
      <input v-model="end" class="input t" type="datetime-local" step="1" />
      <div class="quick">
        <span class="muted">最近</span>
        <button v-for="q in quickList" :key="q.ms" class="btn sm" @click="quick(q.ms)">{{ q.label }}</button>
      </div>
    </div>
    <div class="row">
      <span class="k">关键字</span>
      <input v-model="keyword" class="input kw" :placeholder="kind === 'alarm' ? '报警内容，如 AL556、液面' : '名称、值或颜色'" @keydown.enter="query" />
      <template v-if="kind === 'record'">
        <label class="k" for="record-color">颜色</label>
        <select id="record-color" v-model="color" class="input color-input" :disabled="!availableColors.length">
          <option value="">{{ colorState || '全部颜色' }}</option>
          <option v-for="value in availableColors" :key="value" :value="value">{{ colorLabel(value) }}</option>
        </select>
      </template>
      <div class="acts">
        <button class="btn" :disabled="loading" @click="exportCSV">导出 CSV</button>
        <button class="btn primary" :disabled="loading" @click="query">{{ loading ? '查询中…' : '查询' }}</button>
      </div>
    </div>
    <div class="hint muted">最长 {{ fmtDuration(maxRange / 1000) }}，按上位机时间（北京时间）筛选，结果按 ts 升序排列。开始时间包含在内，结束时间为截止。</div>
    <div v-if="error" class="err">{{ error }}</div>
  </div>

  <div v-if="result && !data" class="card empty"><div class="big">{{ loading ? '查询中…' : '没有读到记录' }}</div></div>
  <div v-else-if="result && !data.total" class="card empty">
    <div class="big">这段时间没有记录</div>
    <div>可以换个时间段、炉台或关键字</div>
  </div>
  <div v-else-if="result" class="card list" :class="{ busy: loading }">
    <div class="lh"><b>{{ result.title }}</b><PageSizePicker v-model="pageSize" :disabled="loading" /></div>
    <div class="wrap">
      <table class="grid">
        <thead><tr><th class="idx">#</th><th v-for="c in data.columns" :key="c.key">{{ c.label }}</th></tr></thead>
        <tbody>
          <tr v-for="(row, i) in data.rows" :key="i" :style="{ '--record-tint': rowTint(row[colorIndex]) }">
            <td class="idx num">{{ (page - 1) * pageSize + i + 1 }}</td>
            <td v-for="(v, j) in row" :key="j" :class="{ num: j === 0 }">{{ v }}</td>
          </tr>
        </tbody>
      </table>
    </div>
    <Pager :model-value="page" class="pagerbar" :total="data.total" :page-size="pageSize" :disabled="loading" @update:model-value="load" />
  </div>
  <div v-else class="card empty">
    <div class="big">选择炉台和时间后点“查询”</div>
    <div>或者点“最近 1 天”等快捷按钮直接查询</div>
  </div>
</template>

<style scoped>
.q { padding: 12px 16px; margin-bottom: 12px }
.row { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; padding: 4px 0 }
.k { font-weight: 700; font-size: 13px; width: 44px }
.t { width: 200px; padding: 6px 10px }
.kw { width: 280px; padding: 6px 10px }
.color-input { width: 150px; padding: 6px 10px }
.grid tbody td { background: var(--record-tint, transparent) }
.grid tbody tr:hover td { background: color-mix(in srgb, var(--record-tint, white) 94%, #c7d2fe) }
.quick { display: flex; align-items: center; gap: 6px; margin-left: 12px; font-size: 12.5px }
.btn.sm { padding: 4px 10px; font-size: 12.5px }
.btn:disabled { opacity: .6; cursor: wait }
.seg { display: inline-flex; gap: 2px; background: #f3f5fb; border-radius: 9px; padding: 2px }
.seg button { border: 0; background: none; padding: 5px 14px; border-radius: 7px; font-weight: 600; font-size: 12.5px; color: var(--muted); cursor: pointer }
.seg button.on { background: #fff; color: var(--brand); box-shadow: 0 1px 2px rgba(15, 23, 42, .1) }
.acts { display: flex; gap: 8px; margin-left: auto }
.hint { font-size: 12px; margin-top: 4px }
.err { margin-top: 8px; color: var(--danger); font-size: 13px; font-weight: 600 }
.list { padding: 10px 14px 12px }
.list.busy .wrap { opacity: .55 }
.lh { display: flex; align-items: center; justify-content: space-between; flex-wrap: wrap; gap: 12px; font-size: 13px; margin-bottom: 12px }
.wrap { overflow-x: auto }
.idx { width: 56px; color: var(--faint) }
td.num { white-space: nowrap }
.pagerbar { margin-top: 10px }
</style>
