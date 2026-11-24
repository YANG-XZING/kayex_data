<script setup>
// 实时曲线：打开页面就开始画——先从数据库补最近一个窗口的数据，再接上每秒推送的新数据；横轴固定为所选窗口长度，往前滚。
// 选中的点位（最多 10 个）画在同一张图里，直接显示完整的图（铺满页面下方，带纵轴，可框选放大、双击还原、
// 点图例隐藏曲线；单击图或点“放大”弹出大图）。离开页面就停，不在后台记录。时间是数据的 ts（服务器时钟）。
import { ref, shallowRef, computed, watch, nextTick, onBeforeUnmount } from 'vue'
import CurveCard from '../components/CurveCard.vue'
import TagPicker from '../components/TagPicker.vue'
import { state, trendColumns, labelOf, save, getJSON, serverNow, fmtDuration } from '../store.js'
import { useCurveCols, toNum } from '../curveCols.js'

const { selected, cols, series, setSelected, maxTags } = useCurveCols()
const picking = ref(false)
const maxMin = computed(() => Math.floor(state.settings.history_max_range_ms / 60000)) // 补数据最多能查多久

// ---------- 窗口 ----------
const PRESETS = [10, 30, 60, 120]
const win = ref(Number(localStorage.getItem('kx.curveWin')) || 30) // 分钟
const custom = ref(!PRESETS.includes(win.value))
const customMin = ref(win.value)
function setWin(m) {
  m = Math.min(Math.max(Math.round(m) || 1, 1), maxMin.value)
  custom.value = !PRESETS.includes(m) || custom.value
  win.value = m
  customMin.value = m
  save('kx.curveWin', m)
}
function pickPreset(m) { custom.value = false; setWin(m) }
const winText = computed(() => (win.value < 60 || win.value % 60 ? `${win.value} 分钟` : `${win.value / 60} 小时`))

// ---------- 数据 ----------
const live = { xs: [], ys: [], cols: [] }
const liveSeries = shallowRef([])
const loading = ref(false)
const error = ref('')
const tick = ref(0)
let pending = null // 补数据期间收到的推送
let gen = 0

function push(tsMs, values) {
  const x = tsMs / 1000
  const n = live.xs.length
  if (n && x <= live.xs[n - 1]) return
  live.xs.push(x)
  live.cols.forEach((c, i) => {
    const v = toNum(values[c])
    const arr = live.ys[i]
    arr.push(v !== null ? v : arr.length ? arr[arr.length - 1] : null) // 空值沿用上一点（部分列更新造成的空值）
  })
  let drop = 0
  while (drop < live.xs.length && x - live.xs[drop] > win.value * 60) drop++
  if (drop) {
    live.xs.splice(0, drop)
    live.ys.forEach((a) => a.splice(0, drop))
  }
}

async function start() {
  const g = ++gen
  error.value = ''
  live.cols = [...cols.value]
  live.xs = []
  live.ys = live.cols.map(() => [])
  liveSeries.value = series.value
  if (!state.current || !live.cols.length) return
  pending = []
  loading.value = true
  const to = serverNow()
  try {
    const qs = new URLSearchParams({ table: state.current, cols: live.cols.join(','), from: to - win.value * 60000, to })
    const d = await getJSON('/api/history?' + qs)
    if (g !== gen) return
    live.xs = d.ts.map((t) => t / 1000)
    live.ys = live.cols.map((c) => (d.values[c] || []).map(toNum))
  } catch (e) {
    if (g !== gen) return
    error.value = '补最近数据失败，只画从现在开始的数据：' + e.message
  }
  for (const r of pending) push(r.ts, r.values)
  pending = null
  loading.value = false
  tick.value++
}
watch(() => state.live.ts, (ts) => {
  if (!ts || state.live.table !== state.current || !live.cols.length) return
  if (pending) pending.push({ ts, values: { ...state.live.values } })
  else {
    push(ts, state.live.values)
    tick.value++
  }
})
const getData = () => [live.xs, ...live.ys]

// 换炉台、点位、窗口时重新开始
watch([() => state.current, () => cols.value.join(), win], start, { immediate: true })
onBeforeUnmount(() => { gen++ })

// ---------- 缩放 ----------
const xRange = ref(null) // 框选放大的时间段，null 为跟随最新
const chart = ref(null)
watch([() => state.current, () => cols.value.join(), win], () => { xRange.value = null })
// 上方条件区高度变化（展开点位选择、自选窗口、错误提示）时，图的高度重新铺满
watch([picking, custom, error], () => nextTick(() => chart.value?.resize()))
</script>

<template>
  <div class="card q">
    <div class="row">
      <span class="k">窗口</span>
      <div class="seg">
        <button v-for="m in PRESETS" :key="m" :class="{ on: !custom && win === m }" @click="pickPreset(m)">{{ m < 60 ? m + ' 分钟' : m / 60 + ' 小时' }}</button>
        <button :class="{ on: custom }" @click="custom = true">自选</button>
      </div>
      <template v-if="custom">
        <input v-model.number="customMin" class="input n" type="number" min="1" :max="maxMin" @keydown.enter="setWin(customMin)" @blur="setWin(customMin)" />
        <span class="muted">分钟（1~{{ maxMin }}）</span>
      </template>
      <span class="muted hint">打开页面先补最近 {{ winText }} 的数据，之后每秒接上新数据</span>
    </div>
    <div class="row">
      <span class="k">点位</span>
      <div class="chips">
        <span v-for="s in series" :key="s.col" class="chip tag" :title="s.col" :style="{ borderColor: s.color }">
          <span class="sw" :style="{ background: s.color }"></span>{{ labelOf(s.col) }}
          <button @click="setSelected(selected.filter((x) => x !== s.col))">×</button></span>
        <button class="btn sm" @click="picking = !picking">{{ picking ? '收起' : '选择点位' }}（{{ cols.length }}/{{ maxTags }}）</button>
      </div>
    </div>
    <div v-if="error" class="err">{{ error }}</div>
  </div>

  <TagPicker v-if="picking" :model-value="selected" :options="trendColumns" :max="maxTags"
    @update:model-value="setSelected" @close="picking = false" />

  <div v-if="!cols.length" class="card empty"><div class="big">还没有选择点位</div><div>点“选择点位”，最多 {{ maxTags }} 个，画在同一张图里</div></div>
  <CurveCard v-else ref="chart" v-model:x-range="xRange" :series="liveSeries" :get-data="getData" :tick="tick" :window-sec="win * 60"
    reset-label="回到实时" tip="单击放大，按住鼠标拖动框选（记录不停），双击或“回到实时”恢复滚动，点图例隐藏曲线">
    <template #head>
      <span class="live-dot"></span><b>实时曲线</b>
      <span class="muted">{{ loading ? '正在补最近数据…' : `最近 ${winText}，每秒更新` }}</span>
    </template>
  </CurveCard>
</template>

<style scoped>
.q { padding: 12px 16px; margin-bottom: 12px }
.row { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; padding: 4px 0; font-size: 12.5px }
.k { font-weight: 700; font-size: 13px; width: 32px }
.n { width: 80px; padding: 5px 8px }
.hint { margin-left: auto; font-size: 12px }
.btn.sm { padding: 4px 10px; font-size: 12.5px }
.seg { display: inline-flex; gap: 2px; background: #f3f5fb; border-radius: 9px; padding: 2px }
.seg button { border: 0; background: none; padding: 4px 12px; border-radius: 7px; font-weight: 600; font-size: 12.5px; color: var(--muted); cursor: pointer }
.seg button.on { background: #fff; color: var(--brand); box-shadow: 0 1px 2px rgba(15, 23, 42, .1) }
.chips { display: flex; gap: 6px; flex-wrap: wrap; align-items: center; flex: 1 }
.tag { padding: 3px 4px 3px 8px; background: #fff; border: 1px solid; gap: 6px; display: inline-flex; align-items: center }
.tag button { border: 0; background: none; color: var(--muted); cursor: pointer; font-size: 14px; line-height: 1; padding: 0 4px }
.sw { width: 12px; height: 3px; border-radius: 2px; flex: none; display: inline-block }
.err { margin-top: 8px; color: var(--danger); font-size: 13px; font-weight: 600 }
.live-dot { width: 8px; height: 8px; border-radius: 50%; background: var(--ok); box-shadow: 0 0 0 3px rgba(16, 185, 129, .2) }
</style>
