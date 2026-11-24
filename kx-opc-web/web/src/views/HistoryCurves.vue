<script setup>
// 历史曲线，两种取法，选中的点位（最多 10 个）都画在同一张图里，画好直接显示完整的图（铺满页面下方，带纵轴，
// 可框选放大、双击还原、点图例隐藏曲线；单击图或点“放大”弹出大图）：
//   按时间：选时间段（最长 2 小时），逐行画；可导出同样条件的 CSV。
//   按晶棒：选一根晶，可再勾选工步（不勾为整棒）。一根晶常有一两天，曲线按时间均分抽样（每段取最后一个值），
//          背后用颜色标出工步；导出 CSV 是原始全部数据（所选点位或全部点位、所选工步）。
// 时间是数据的 ts（服务器时钟）。
import { ref, shallowRef, computed, watch, nextTick } from 'vue'
import { useRoute } from 'vue-router'
import CurveCard from '../components/CurveCard.vue'
import TagPicker from '../components/TagPicker.vue'
import { state, currentFurnace, trendColumns, labelOf, save, getJSON, toInput, fromInput, fmtDuration, serverNow } from '../store.js'
import { useCurveCols, toNum } from '../curveCols.js'
import { stepColor } from '../colors.js'

const props = defineProps({ mode: { type: String, default: 'time' } }) // 侧栏二级菜单：按时间 / 按晶棒
const { selected, cols, series, seriesOf, setSelected, maxTags } = useCurveCols()
const picking = ref(false)
const maxRange = computed(() => state.settings.history_max_range_ms)
const mode = computed(() => props.mode)
const error = ref('')
const loading = ref(false)
const result = shallowRef(null) // { cols, xs, ys, rows, title, bucket, bands }
const tick = ref(0)
const resultSeries = computed(() => (result.value ? seriesOf(result.value.cols) : []))
const getData = () => [result.value.xs, ...result.value.ys]

function toResult(d, extra) {
  const c = [...cols.value]
  let xs = d.ts.map((t) => t / 1000)
  let ys = c.map((x) => (d.values[x] || []).map(toNum))
  // 去掉首尾所有点位都没值的部分（按晶棒抽样时没数据的时间段也有点），横轴从有数据的地方开始，曲线铺满整张图
  const has = (i) => ys.some((y) => y[i] !== null && y[i] !== undefined)
  let lo = 0
  let hi = xs.length - 1
  while (lo <= hi && !has(lo)) lo++
  while (hi >= lo && !has(hi)) hi--
  if (lo > 0 || hi < xs.length - 1) {
    xs = xs.slice(lo, hi + 1)
    ys = ys.map((y) => y.slice(lo, hi + 1))
  }
  xRange.value = null
  tick.value++
  return { cols: c, xs, ys, rows: xs.length ? d.rows : 0, ...extra }
}

// ---------- 按时间 ----------
const QUICK = [10, 30, 60, 120].map((m) => ({ label: m < 60 ? `${m} 分钟` : `${m / 60} 小时`, ms: m * 60000 }))
const end = ref(toInput(serverNow()))
const start = ref(toInput(serverNow() - 3600000))
function timeCheck() {
  const from = fromInput(start.value)
  const to = fromInput(end.value)
  if (!cols.value.length) return [null, '请选择点位']
  if (!(from < to)) return [null, '开始时间必须早于结束时间']
  if (to - from > maxRange.value) return [null, `时间范围不能超过 ${fmtDuration(maxRange.value / 1000)}`]
  return [new URLSearchParams({ table: state.current, cols: cols.value.join(','), from, to }), '']
}
async function queryTime() {
  const [qs, err] = timeCheck()
  error.value = err
  if (!qs) return
  loading.value = true
  try {
    const d = await getJSON('/api/history?' + qs)
    result.value = toResult(d, { title: `${start.value.replace('T', ' ')} 至 ${end.value.replace('T', ' ')}` })
  } catch (e) {
    error.value = '查询失败：' + e.message
  } finally {
    loading.value = false
  }
}
function quick(ms) {
  const e = Math.floor(serverNow() / 1000) * 1000
  end.value = toInput(e)
  start.value = toInput(e - ms)
  queryTime()
}
function exportTime() {
  const [qs, err] = timeCheck()
  error.value = err
  if (qs) location.href = '/api/history/export?' + qs
}

// ---------- 按晶棒 ----------
const crystals = ref([])
const crystal = ref(null)
const stats = ref(null) // /api/crystals/steps 的结果
const steps = ref(new Set()) // 勾选的工步号，空为整棒
const short = (no) => (no ? no.slice(0, 8) : '—')
const p2 = (n) => String(n).padStart(2, '0')
const md = (ms) => { const d = new Date(ms); return `${p2(d.getMonth() + 1)}-${p2(d.getDate())} ${p2(d.getHours())}:${p2(d.getMinutes())}` }
const range = (c) => ({ from: c.first_row, to: c.ongoing ? Math.max(c.last_row, serverNow()) : c.last_row })

async function loadCrystals() {
  crystals.value = []
  if (!state.current || mode.value !== 'crystal') return
  try {
    const d = await getJSON(`/api/crystals?table=${state.current}&days=30`)
    crystals.value = d.crystals
    if (!crystal.value || !d.crystals.some((c) => c.crystal_no === crystal.value.crystal_no)) pickCrystal(d.crystals[0] || null)
  } catch (e) {
    error.value = '读取晶棒列表失败：' + e.message
  }
}
async function pickCrystal(c) {
  crystal.value = c
  stats.value = null
  steps.value = new Set()
  result.value = null
  if (!c) return
  try {
    const r = range(c)
    stats.value = await getJSON(`/api/crystals/steps?table=${state.current}&crystal=${c.crystal_no}&from=${r.from}&to=${r.to}`)
  } catch (e) {
    error.value = '读取工步失败：' + e.message
  }
}
function toggleStep(code) {
  const n = new Set(steps.value)
  n.has(code) ? n.delete(code) : n.add(code)
  steps.value = n
}
const stepList = () => [...steps.value].sort((a, b) => Number(a) - Number(b)).join(',')
// 查询范围：整棒为整根晶；选了工步时只取这些工步第一次进入到最后一次离开，曲线铺满横轴、抽样也更细
function crystalRange() {
  const r = range(crystal.value)
  const runs = stats.value?.runs || []
  if (!steps.value.size || !runs.length) return r
  let from = Infinity
  let to = -Infinity
  runs.forEach((run, i) => {
    if (!steps.value.has(run.code)) return
    from = Math.min(from, run.start)
    to = Math.max(to, i + 1 < runs.length ? runs[i + 1].start - 1 : r.to)
  })
  return Number.isFinite(from) ? { from, to } : r
}
function crystalQS(extra = {}) {
  const r = crystalRange()
  const qs = new URLSearchParams({ table: state.current, crystal: crystal.value.crystal_no, from: r.from, to: r.to, ...extra })
  if (steps.value.size) qs.set('steps', stepList())
  return qs
}
// 工步色带：时间线上每一段（勾了工步时只标这些工步）
function bandsOf(st, last) {
  const runs = st?.runs || []
  return runs.map((r, i) => ({ code: r.code, from: r.start / 1000, to: (i + 1 < runs.length ? runs[i + 1].start : last) / 1000 }))
    .filter((b) => !steps.value.size || steps.value.has(b.code))
    .map((b) => ({ ...b, color: stepColor(b.code) }))
}
async function queryCrystal() {
  error.value = !crystal.value ? '请选择晶棒' : !cols.value.length ? '请选择点位' : ''
  if (error.value) return
  loading.value = true
  try {
    const d = await getJSON('/api/crystals/series?' + crystalQS({ cols: cols.value.join(','), points: 2000 }))
    const c = crystal.value
    result.value = toResult(d, {
      title: `晶棒 #${short(c.crystal_no)}${steps.value.size ? ' · 工步 ' + stepList() : ' · 整棒'}`,
      bucket: d.bucket_s, bands: bandsOf(stats.value, range(c).to),
    })
  } catch (e) {
    error.value = '查询失败：' + e.message
  } finally {
    loading.value = false
  }
}
function exportCrystal(allCols) {
  error.value = !crystal.value ? '请选择晶棒' : !allCols && !cols.value.length ? '请选择点位' : ''
  if (error.value) return
  location.href = '/api/crystals/export?' + crystalQS(allCols ? {} : { cols: cols.value.join(',') })
}

watch(() => state.current, () => { crystal.value = null; result.value = null; loadCrystals() }, { immediate: true })

// ---------- 缩放 ----------
const xRange = ref(null) // 框选放大的时间段，null 为全部
const chart = ref(null)
// 上方条件区高度变化（展开点位选择、出现错误提示）时，图的高度重新铺满
watch([picking, error, () => stats.value?.steps?.length], () => nextTick(() => chart.value?.resize()))

// 从告警记录“看曲线”跳过来时带着 ?from=&to=：按时间查询
const route = useRoute()
const fromQ = Number(route.query.from)
const toQ = Number(route.query.to)
if (fromQ && toQ) {
  start.value = toInput(fromQ)
  end.value = toInput(toQ)
  let done = false
  watch(() => trendColumns.value.length > 0 && !!currentFurnace.value, (ready) => {
    if (ready && !done) { done = true; queryTime() }
  }, { immediate: true })
}
</script>

<template>
  <div class="card q">
    <div v-if="mode === 'time'" class="row">
      <span class="k">时间</span>
      <input v-model="start" class="input t" type="datetime-local" step="1" />
      <span class="muted">至</span>
      <input v-model="end" class="input t" type="datetime-local" step="1" />
      <span class="muted">最近</span>
      <button v-for="x in QUICK" :key="x.ms" class="btn sm" @click="quick(x.ms)">{{ x.label }}</button>
    </div>

    <template v-else>
      <div class="row">
        <span class="k">晶棒</span>
        <div class="crys">
          <button v-for="c in crystals" :key="c.crystal_no" class="cc" :class="{ on: crystal?.crystal_no === c.crystal_no }" @click="pickCrystal(c)">
            <span class="dot" :class="c.ongoing ? 'live' : 'done'"></span>
            <b class="num">{{ fmtDuration(c.seconds) }}</b>
            <span class="muted">{{ md(c.start) }} 起</span>
            <span v-if="c.is_first" class="chip brand xs">首棒</span>
          </button>
          <span v-if="!crystals.length" class="muted">最近 30 天没有带晶编号的数据</span>
        </div>
      </div>
      <div v-if="stats" class="row">
        <span class="k">工步</span>
        <div class="steps">
          <button v-for="s in stats.steps" :key="s.code" class="sc" :class="{ on: steps.has(s.code) }" :title="`${s.code} ${s.name}，进入 ${s.visits} 次`"
            @click="toggleStep(s.code)">
            <span class="sw2" :style="{ background: stepColor(s.code) }"></span>{{ s.name }}<span class="muted num">{{ fmtDuration(s.seconds) }}</span>
          </button>
          <span class="muted">{{ steps.size ? `已选 ${steps.size} 个工步` : '不选为整棒' }}</span>
          <button v-if="steps.size" class="link" @click="steps = new Set()">清空</button>
        </div>
      </div>
    </template>

    <div class="row">
      <span class="k">点位</span>
      <div class="chips">
        <span v-for="s in series" :key="s.col" class="chip tag" :title="s.col" :style="{ borderColor: s.color }">
          <span class="sw" :style="{ background: s.color }"></span>{{ labelOf(s.col) }}
          <button @click="setSelected(selected.filter((x) => x !== s.col))">×</button></span>
        <button class="btn sm" @click="picking = !picking">{{ picking ? '收起' : '选择点位' }}（{{ cols.length }}/{{ maxTags }}）</button>
      </div>
      <div class="acts">
        <template v-if="mode === 'time'">
          <button class="btn" :disabled="loading" @click="exportTime">导出 CSV</button>
          <button class="btn primary" :disabled="loading" @click="queryTime">{{ loading ? '查询中…' : '画曲线' }}</button>
        </template>
        <template v-else>
          <button class="btn" @click="exportCrystal(false)" title="原始全部数据，只含这几个点位">导出所选点位</button>
          <button class="btn" @click="exportCrystal(true)" title="原始全部数据，全部点位">导出全部点位</button>
          <button class="btn primary" :disabled="loading" @click="queryCrystal">{{ loading ? '查询中…' : '画曲线' }}</button>
        </template>
      </div>
    </div>
    <div v-if="error" class="err">{{ error }}</div>
  </div>

  <TagPicker v-if="picking" :model-value="selected" :options="trendColumns" :max="maxTags"
    @update:model-value="setSelected" @close="picking = false" />

  <div v-if="!result" class="card empty">
    <div class="big">{{ mode === 'time' ? '选择时间和点位后点“画曲线”' : '选择晶棒（和工步）、点位后点“画曲线”' }}</div>
    <div>{{ mode === 'time' ? '或者点“最近 1 小时”等快捷按钮' : '导出不用先画曲线，直接点导出即可' }}</div>
  </div>
  <div v-else-if="!result.rows" class="card empty"><div class="big">没有数据</div><div>炉台可能离线，或者条件选错了</div></div>
  <CurveCard v-else ref="chart" v-model:x-range="xRange" :series="resultSeries" :get-data="getData" :tick="tick" :bands="result.bands"
    tip="单击放大，按住鼠标拖动框选，双击还原，点图例隐藏曲线">
    <template #head>
      <b>历史曲线</b><span class="muted">{{ result.title }}</span>
      <span v-if="result.bucket > 1" class="muted">· 已抽样，每 {{ fmtDuration(result.bucket) }}一个点</span>
    </template>
  </CurveCard>
</template>

<style scoped>
.q { padding: 12px 16px; margin-bottom: 12px }
.modes { display: grid; grid-template-columns: 1fr 1fr; gap: 8px; margin-bottom: 8px }
.modes button { display: flex; flex-direction: column; align-items: flex-start; gap: 2px; padding: 8px 14px; border: 1px solid var(--line);
  border-radius: 12px; background: #fff; cursor: pointer; text-align: left }
.modes button b { font-size: 14px; color: #334155 }
.modes button span { font-size: 11.5px; color: var(--muted) }
.modes button.on { border-color: var(--brand); background: var(--brand-soft) }
.modes button.on b { color: var(--brand) }
.row { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; padding: 4px 0; font-size: 12.5px }
.k { font-weight: 700; font-size: 13px; width: 32px; flex: none }
.t { width: 200px; padding: 6px 10px }
.btn.sm { padding: 4px 10px; font-size: 12.5px }
.btn:disabled { opacity: .6; cursor: wait }
.chips, .steps, .crys { display: flex; gap: 6px; flex-wrap: wrap; align-items: center; flex: 1 }
.crys { flex-wrap: nowrap; overflow-x: auto; padding-bottom: 4px }
.cc { flex: none; display: flex; align-items: center; gap: 6px; border: 1px solid var(--line); background: #fff; border-radius: 10px; padding: 5px 10px;
  cursor: pointer; font-size: 12px; white-space: nowrap }
.cc b { font-size: 13px }
.cc.on { border-color: var(--brand); background: var(--brand-soft) }
.dot { width: 7px; height: 7px; border-radius: 50%; flex: none }
.dot.live { background: var(--ok) }
.dot.done { background: #cbd5e1 }
.chip.xs { font-size: 10.5px; padding: 0 6px }
.sc { display: inline-flex; align-items: center; gap: 6px; border: 1px solid var(--line); background: #fff; border-radius: 999px; padding: 3px 10px;
  cursor: pointer; font-size: 12px; font-weight: 600; color: #334155 }
.sc .muted { font-weight: 500; font-size: 11px }
.sc.on { border-color: var(--brand); background: var(--brand-soft); color: var(--brand) }
.sw2 { width: 8px; height: 8px; border-radius: 2px }
.link { border: 0; background: none; color: var(--brand); cursor: pointer; font-weight: 600; font-size: 12px }
.tag { padding: 3px 4px 3px 8px; background: #fff; border: 1px solid; gap: 6px; display: inline-flex; align-items: center }
.tag button { border: 0; background: none; color: var(--muted); cursor: pointer; font-size: 14px; line-height: 1; padding: 0 4px }
.sw { width: 12px; height: 3px; border-radius: 2px; flex: none; display: inline-block }
.acts { display: flex; gap: 8px; margin-left: auto }
.err { margin-top: 8px; color: var(--danger); font-size: 13px; font-weight: 600 }
</style>
