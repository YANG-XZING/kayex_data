<script setup>
// 多个点位画在同一张图里（uPlot，Canvas 绘制）。实时和历史共用：
//   getData() 返回 [xs(秒), ys1, ys2, …]，顺序与 series 一致；tick 变化时重画，页面不可见时不画。
//   每个点位各用各的纵轴（开关量共用一根 0/1 轴），量纲不同的曲线也能看清形状。
//   interactive：显示纵轴、可框选放大（zoom 事件交给父组件），双击还原；缩略图不显示纵轴、不能交互。
//   windowSec：实时曲线的窗口长度，横轴固定显示最近这么长，随新数据往前滚。
//   bands：在曲线背后按时间段画淡色底和顶部色条，用来标出工步。
//   fill：高度铺满窗口里剩下的空间（随窗口大小变化），height 作为最小高度。
//   plotclick：在图上单击（没有拖动、也不是双击）时触发，页面上的图用它打开放大窗口。
import { ref, computed, watch, onMounted, onBeforeUnmount } from 'vue'
import uPlot from 'uplot'
import { labelOf, fmtField } from '../store.js'

const props = defineProps({
  series: Array, // [{ col, color, bool }]
  getData: Function,
  tick: Number,
  height: { type: Number, default: 260 },
  interactive: Boolean,
  xRange: Array, // [min, max] 秒，null 为全部
  windowSec: Number,
  bands: Array, // [{ from, to, color }] 秒
  fill: Boolean,
})
const emit = defineEmits(['zoom', 'plotclick'])

const FILL_BOTTOM = 28 // 铺满时给卡片下边距留的空间
const box = ref(null)
const fillHeight = ref(0)
// 实际绘图高度：铺满时为窗口底部到图顶部的距离（不小于 height）
const plotHeight = () => (props.fill ? Math.max(props.height, fillHeight.value) : props.height)
function measureFill() {
  if (!props.fill || !box.value) return
  fillHeight.value = Math.round(window.innerHeight - box.value.getBoundingClientRect().top - FILL_BOTTOM)
}
const cursorIdx = ref(null)
const hidden = ref(new Set())
const lastVals = ref([])
let plot = null
let ro = null

const scaleOf = (s) => (s.bool ? 'bool' : 'c:' + s.col)

const p2 = (n) => String(n).padStart(2, '0')
function clockLabel(sec) {
  const d = new Date(sec * 1000)
  return `${p2(d.getHours())}:${p2(d.getMinutes())}:${p2(d.getSeconds())}`
}
// 横轴跨度超过 20 小时（一根晶常有一两天）时带上日期
function axisLabel(u, sec) {
  if (u.scales.x.max - u.scales.x.min < 20 * 3600) return clockLabel(sec)
  const d = new Date(sec * 1000)
  return `${p2(d.getMonth() + 1)}-${p2(d.getDate())} ${p2(d.getHours())}:${p2(d.getMinutes())}`
}

// 工步底色：整个绘图区淡淡一层，顶部一条实色条
function drawBands(u) {
  if (!props.bands?.length) return
  const { ctx } = u
  const { top, height, left, width } = u.bbox
  ctx.save()
  for (const b of props.bands) {
    const x0 = Math.max(left, u.valToPos(b.from, 'x', true))
    const x1 = Math.min(left + width, u.valToPos(b.to, 'x', true))
    if (x1 <= x0) continue
    ctx.fillStyle = b.color
    ctx.globalAlpha = 0.08
    ctx.fillRect(x0, top, x1 - x0, height)
    ctx.globalAlpha = 0.85
    ctx.fillRect(x0, top, x1 - x0, 5 * devicePixelRatio)
  }
  ctx.restore()
}

function options(width) {
  const axis = { stroke: '#64748b', grid: { stroke: '#eef1f7', width: 1 }, ticks: { stroke: '#e5eaf2', width: 1 }, font: '11px sans-serif' }
  const scales = { x: { time: true } }
  const yAxes = []
  for (const s of props.series) {
    const key = scaleOf(s)
    if (scales[key]) continue
    scales[key] = key === 'bool' ? { range: [-0.1, 1.1] } : { auto: true }
    // 纵轴最多 4 根，左右交替；同一单位只画一根，颜色用这一组第一条曲线的颜色
    if (props.interactive && yAxes.length < 4) {
      yAxes.push({ ...axis, scale: key, side: yAxes.length % 2 ? 1 : 3, stroke: s.color, size: 58,
        grid: { ...axis.grid, show: yAxes.length === 0 },
        values: key === 'bool' ? (u, splits) => splits.map((v) => (v === 0 ? '关' : v === 1 ? '开' : '')) : undefined })
    }
  }
  return {
    width,
    height: plotHeight(),
    cursor: {
      drag: props.interactive ? { x: true, y: false, setScale: false } : { x: false, y: false },
      points: { show: props.interactive },
      show: props.interactive,
    },
    select: { show: props.interactive },
    legend: { show: false },
    scales,
    series: [{}, ...props.series.map((s) => ({
      // spanGaps：中间缺数（断数、被工步筛掉的时间）时把前后两点连起来，曲线不断开
      label: s.col, stroke: s.color, width: 1.5, scale: scaleOf(s), points: { show: false }, show: !hidden.value.has(s.col), spanGaps: true,
    }))],
    axes: [
      { ...axis, values: (u, splits) => splits.map((s) => axisLabel(u, s)) },
      ...(yAxes.length ? yAxes : [{ scale: scaleOf(props.series[0] || { col: '' }), show: false }]),
    ],
    padding: props.interactive ? undefined : [8, 8, 0, 8],
    hooks: {
      drawClear: [drawBands],
      setCursor: [(u) => { cursorIdx.value = u.cursor.idx ?? null }],
      setSelect: [(u) => {
        if (!props.interactive || u.select.width < 5) return
        const min = u.posToVal(u.select.left, 'x')
        const max = u.posToVal(u.select.left + u.select.width, 'x')
        u.setSelect({ left: 0, width: 0, top: 0, height: 0 }, false)
        emit('zoom', [min, max])
      }],
    },
  }
}

let data = [[]]
function draw() {
  if (!plot || document.hidden) return
  data = props.getData()
  const xs = data[0] || []
  const follow = !props.xRange && props.windowSec && xs.length
  plot.setData(data, !props.xRange && !follow)
  if (props.xRange) plot.setScale('x', { min: props.xRange[0], max: props.xRange[1] })
  else if (follow) {
    // 实时窗口：横轴固定为最近 windowSec 秒（数据不满一个窗口时从第一个点开始）
    const last = xs[xs.length - 1]
    const min = Math.max(xs[0], last - props.windowSec)
    plot.setScale('x', { min, max: min + props.windowSec })
  }
  // 每条曲线最后一个有值的点，图例在不悬停时显示它
  lastVals.value = props.series.map((s, i) => {
    const ys = data[i + 1] || []
    for (let k = ys.length - 1; k >= 0; k--) if (ys[k] !== null && ys[k] !== undefined) return ys[k]
    return null
  })
}

function build() {
  plot?.destroy()
  plot = new uPlot(options(box.value.clientWidth || 600), [[], ...props.series.map(() => [])], box.value)
  if (props.interactive) plot.over.addEventListener('dblclick', () => { clearTimeout(clickTimer); emit('zoom', null) })
  plot.over.addEventListener('mousedown', (e) => { downAt = [e.clientX, e.clientY] })
  plot.over.addEventListener('click', onPlotClick)
  draw()
}

// 单击：按下和松开几乎在同一处才算（拖动是框选放大）；等一下看是不是双击（双击是还原缩放）
const CLICK_MOVE = 4
const DBLCLICK_WAIT = 250
let downAt = null
let clickTimer = 0
function onPlotClick(e) {
  if (!downAt || Math.abs(e.clientX - downAt[0]) > CLICK_MOVE || Math.abs(e.clientY - downAt[1]) > CLICK_MOVE) return
  if (e.detail > 1) return
  clearTimeout(clickTimer)
  clickTimer = setTimeout(() => emit('plotclick'), DBLCLICK_WAIT)
}

function toggle(s, i) {
  const h = new Set(hidden.value)
  h.has(s.col) ? h.delete(s.col) : h.add(s.col)
  hidden.value = h
  plot?.setSeries(i + 1, { show: !h.has(s.col) })
}

const legend = computed(() => props.series.map((s, i) => {
  const idx = cursorIdx.value
  const v = idx === null ? lastVals.value[i] : data[i + 1]?.[idx]

  return { ...s, i, label: labelOf(s.col), value: s.bool && v != null ? (v ? '开' : '关') : fmtField(s.col, v) }
}))
const cursorTime = computed(() => (cursorIdx.value === null || !data[0]?.length ? '' : clockLabel(data[0][cursorIdx.value])))

function onVisible() { if (!document.hidden) draw() }
function resize() {
  measureFill()
  const w = box.value?.clientWidth
  if (w && plot) plot.setSize({ width: w, height: plotHeight() })
}

onMounted(() => {
  measureFill()
  build()
  ro = new ResizeObserver(resize)
  ro.observe(box.value)
  window.addEventListener('resize', resize)
  document.addEventListener('visibilitychange', onVisible)
})
onBeforeUnmount(() => {
  clearTimeout(clickTimer)
  document.removeEventListener('visibilitychange', onVisible)
  window.removeEventListener('resize', resize)
  ro?.disconnect()
  plot?.destroy()
  plot = null
})

watch(() => props.tick, draw)
watch(() => props.xRange, draw)
watch(() => props.windowSec, draw)
// 不能用 plot.redraw()：它会按当前（旧的）横轴范围重设横轴，盖掉同一轮里 draw() 刚设的新范围
watch(() => props.bands, draw)
watch(() => props.series.map((s) => s.col + s.color).join(), build)
watch(() => props.height, resize)
defineExpose({ resize }) // 上方内容高度变了（如展开点位选择）时由父组件调用
</script>

<template>
  <div class="mc">
    <div class="legend">
      <button v-for="s in legend" :key="s.col" class="lg" :class="{ off: hidden.has(s.col) }" :title="`${s.col}${interactive ? '（点击隐藏 / 显示）' : ''}`"
        @click.stop="interactive && toggle(s, s.i)">
        <span class="sw" :style="{ background: s.color }"></span>
        <span class="nm">{{ s.label }}</span>
        <b class="num">{{ s.value }}</b>
      </button>
      <span v-if="cursorTime" class="ct num">{{ cursorTime }}</span>
    </div>
    <div ref="box" class="plot" :style="{ minHeight: plotHeight() + 'px' }"></div>
  </div>
</template>

<style scoped>
.legend { display: flex; flex-wrap: wrap; gap: 4px 6px; align-items: center; margin-bottom: 6px }
.lg { display: inline-flex; align-items: center; gap: 6px; border: 1px solid var(--line); background: #fff; border-radius: 8px;
  padding: 3px 9px; font-size: 12px; cursor: inherit; max-width: 280px }
.lg.off { opacity: .4 }
.lg .nm { color: #334155; font-weight: 600; overflow: hidden; text-overflow: ellipsis; white-space: nowrap }
.lg b { font-size: 12.5px }
.sw { width: 12px; height: 3px; border-radius: 2px; flex: none }
.ct { margin-left: auto; font-size: 12px; color: var(--muted) }
.plot { width: 100% }
</style>
