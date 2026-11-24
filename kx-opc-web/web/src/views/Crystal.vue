<script setup>
// 按晶棒导出：左边是当前炉台最近的晶棒（kx-inner-processor 给每行打的晶编号 + 晶棒台账），
// 右边是选中晶棒的工步时间线和工步统计，可整棒导出，或只导出勾选的工步（如只要等径段）。
// 时长都按上位机心跳时间算；工步常常来回跳（如 23.0 ↔ 24.1 交替多次），工步时长是每次停留的总和，并标出进入次数。
import { ref, computed, watch } from 'vue'
import FurnaceHeader from '../components/FurnaceHeader.vue'
import TagPicker from '../components/TagPicker.vue'
import { state, currentFurnace, getJSON, fmtDuration, serverNow, save } from '../store.js'
import { stepColor } from '../colors.js'

const days = ref(Number(localStorage.getItem('kx.crystal.days')) || 30)
const listDays = ref([7, 30, 90])
const loading = ref(false)
const error = ref('')
const crystals = ref([])
const selected = ref(null)

async function load() {
  if (!state.current) return
  loading.value = true
  error.value = ''
  try {
    const d = await getJSON(`/api/crystals?table=${state.current}&days=${days.value}`)
    crystals.value = d.crystals
    listDays.value = d.list_days
    const keep = selected.value && d.crystals.find((c) => c.crystal_no === selected.value.crystal_no)
    if (keep) selected.value = keep
    else if (d.crystals.length) pick(d.crystals[0])
    else selected.value = null
  } catch (e) {
    error.value = '读取晶棒列表失败：' + e.message
  } finally {
    loading.value = false
  }
}

const short = (no) => (no ? no.slice(0, 8) : '—')
const range = (c) => ({ from: c.first_row, to: c.ongoing ? Math.max(c.last_row, serverNow()) : c.last_row })
const p2 = (n) => String(n).padStart(2, '0')
const md = (ms) => { const d = new Date(ms); return `${p2(d.getMonth() + 1)}-${p2(d.getDate())} ${p2(d.getHours())}:${p2(d.getMinutes())}` }
const kw = (v) => (v == null ? '—' : `${Number(v.toFixed(2))} kW`)
// 选中晶棒的台账时间点：没有的不显示；炉次的熔料 / 预热时间点只在首棒上显示
const marks = computed(() => {
  const c = selected.value
  if (!c) return []
  const out = []
  if (c.is_first) {
    if (c.melt_ts) out.push({ k: '本炉进入熔料', v: md(c.melt_ts) })
    if (c.preheat_prep_ts) out.push({ k: '本炉进入预热准备', v: md(c.preheat_prep_ts) })
    if (c.preheat_dip_ts) out.push({ k: '本炉进入预热熔接', v: md(c.preheat_dip_ts) })
  }
  if (c.in_ledger) out.push({ k: '开始（7/8）', v: md(c.start) })
  if (c.neck_ts) {
    out.push({ k: '进入引晶', v: md(c.neck_ts) })
    out.push({ k: '引晶时主加功率', v: kw(c.neck_main_power) })
    out.push({ k: '引晶功率设定值', v: kw(c.neck_power_sp) })
  }
  if (c.body_ts) out.push({ k: '进入等径', v: md(c.body_ts) })
  return out
})

// ---------- 选中的晶棒：工步统计 + 时间线 ----------
const stats = ref(null) // { steps, runs, seconds, gap_seconds, rows }
const statsLoading = ref(false)
const chosen = ref(new Set())
let pickSeq = 0
async function pick(c) {
  const seq = ++pickSeq
  selected.value = c
  stats.value = null
  chosen.value = new Set()
  statsLoading.value = true
  try {
    const r = range(c)
    const d = await getJSON(`/api/crystals/steps?table=${state.current}&crystal=${c.crystal_no}&from=${r.from}&to=${r.to}`)
    if (seq === pickSeq) stats.value = d
  } catch (e) {
    if (seq === pickSeq) error.value = '读取工步失败：' + e.message
  } finally {
    if (seq === pickSeq) statsLoading.value = false
  }
}
// 换炉台、换天数时重新读列表（放在 stats 等声明之后：immediate 会立即执行）
watch(() => state.current, () => { selected.value = null; crystals.value = []; stats.value = null; load() }, { immediate: true })
watch(days, (d) => { save('kx.crystal.days', d); load() })

function toggleStep(code) {
  const n = new Set(chosen.value)
  n.has(code) ? n.delete(code) : n.add(code)
  chosen.value = n
}
function selectSteps(f) { chosen.value = new Set((stats.value?.steps || []).filter(f).map((s) => s.code)) }
const total = computed(() => stats.value?.seconds || 0)
const maxStep = computed(() => Math.max(1, ...(stats.value?.steps || []).map((s) => s.seconds)))
const jumpy = computed(() => (stats.value?.steps || []).filter((s) => s.visits > 1).length)
// 时间线：每一段按停留时长占宽度，太短的至少 2px，看得见来回跳
const timeline = computed(() => {
  const runs = stats.value?.runs || []
  const sum = runs.reduce((n, r) => n + r.seconds, 0) || 1
  const visit = {}
  return runs.map((r) => {
    visit[r.code] = (visit[r.code] || 0) + 1
    const s = stats.value.steps.find((x) => x.code === r.code)
    return { ...r, pct: (r.seconds / sum) * 100, name: s?.name || '', nth: visit[r.code], times: s?.visits || 1 }
  })
})
const hoverCode = ref('')

// ---------- 导出 ----------
const colMode = ref(localStorage.getItem('kx.crystal.colMode') === 'custom' ? 'custom' : 'all')
watch(colMode, (m) => localStorage.setItem('kx.crystal.colMode', m))
const allCols = computed(() => state.columns.map((c) => c.name))
const custom = ref(JSON.parse(localStorage.getItem('kx.crystal.cols') || '[]'))
function setCustom(list) { custom.value = list; save('kx.crystal.cols', list) }
const picking = ref(false)
const exportCols = computed(() => (colMode.value === 'custom' ? custom.value.filter((c) => allCols.value.includes(c)) : []))
const chosenRows = computed(() => (stats.value?.steps || []).filter((s) => chosen.value.has(s.code)).reduce((n, s) => n + s.rows, 0))

function exportCSV(onlySteps) {
  const c = selected.value
  if (!c) return
  if (colMode.value === 'custom' && !exportCols.value.length) { error.value = '请先选择要导出的点位'; return }
  error.value = ''
  const r = range(c)
  const qs = new URLSearchParams({ table: state.current, crystal: c.crystal_no, from: r.from, to: r.to })
  if (onlySteps) qs.set('steps', [...chosen.value].sort((a, b) => Number(a) - Number(b)).join(','))
  if (exportCols.value.length) qs.set('cols', exportCols.value.join(','))
  location.href = '/api/crystals/export?' + qs
}
</script>

<template>
  <div v-if="!currentFurnace" class="card empty">
    <div class="big">请先选择炉台</div>
    <div>在上方输入炉台号或 IP</div>
  </div>
  <template v-else>
    <FurnaceHeader />
    <div class="cg">
      <!-- 晶棒列表 -->
      <div class="card list">
        <div class="hd">
          <span>晶棒</span><span class="muted cnt">{{ crystals.length }} 根</span>
          <div class="seg">
            <button v-for="d in listDays" :key="d" :class="{ on: days === d }" @click="days = d">{{ d }} 天</button>
          </div>
          <button class="btn sm" :disabled="loading" title="刷新" @click="load">{{ loading ? '…' : '刷新' }}</button>
        </div>
        <div v-if="error && !selected" class="err pad">{{ error }}</div>
        <div class="items">
          <button v-for="c in crystals" :key="c.crystal_no" class="ci" :class="{ on: selected?.crystal_no === c.crystal_no }" @click="pick(c)">
            <div class="ci-top">
              <span class="dot" :class="c.ongoing ? 'live' : 'done'"></span>
              <span class="st">{{ c.ongoing ? '进行中' : '已结束' }}</span>
              <span v-if="c.is_first" class="chip brand xs">首棒</span>
              <span v-if="c.seq" class="chip xs mute">第 {{ c.seq }} 棒</span>
              <span class="mono muted no" :title="c.crystal_no">#{{ short(c.crystal_no) }}</span>
            </div>
            <div class="ci-dur num">{{ fmtDuration(c.seconds) }}</div>
            <div class="ci-meta muted"><span>{{ md(c.start) }} 起</span><span class="num">{{ c.rows.toLocaleString() }} 行</span></div>
          </button>
          <div v-if="!loading && !crystals.length" class="empty sm">这段时间没有带晶编号的数据<br />（kx-inner-processor 处理过的数据才有晶编号）</div>
        </div>
      </div>

      <!-- 选中的晶棒 -->
      <div v-if="selected" class="card detail">
        <div class="dh">
          <div>
            <div class="title">晶棒 <span class="mono">#{{ short(selected.crystal_no) }}</span>
              <span class="chip xs" :class="selected.ongoing ? 'ok' : 'mute'">{{ selected.ongoing ? '进行中' : '已结束' }}</span>
              <span v-if="selected.is_first" class="chip brand xs">首棒</span>
              <span v-if="selected.seq" class="chip xs mute">第 {{ selected.seq }} 棒</span>
            </div>
            <div class="muted sub mono" :title="'炉次号 ' + selected.furnace_no">炉次 {{ short(selected.furnace_no) }}</div>
          </div>
          <div class="tiles">
            <div class="tile"><div class="k">时长</div><div class="v num">{{ fmtDuration(total || selected.seconds) }}</div></div>
            <div class="tile"><div class="k">数据</div><div class="v num">{{ (stats?.rows ?? selected.rows).toLocaleString() }}<small> 行</small></div></div>
            <div class="tile"><div class="k">工步</div><div class="v num">{{ stats?.steps.length ?? '—' }}<small v-if="jumpy"> · {{ jumpy }} 个来回跳</small></div></div>
            <div v-if="stats?.gap_seconds" class="tile warn"><div class="k">断数</div><div class="v num">{{ fmtDuration(stats.gap_seconds) }}</div></div>
          </div>
        </div>

        <!-- 台账里的时间点：晶棒的引晶 / 等径，首棒再加上本炉的初始熔料阶段 -->
        <div v-if="marks.length" class="marks">
          <div v-for="m in marks" :key="m.k" class="mk"><span class="k">{{ m.k }}</span><span class="v num">{{ m.v }}</span></div>
        </div>

        <div v-if="statsLoading" class="empty sm">正在统计工步…</div>
        <template v-else-if="stats">
          <div class="sec">
            <div class="sec-h">工步时间线<span class="muted">每一段是一次连续停留，宽度按时长；点一段可勾选这个工步</span></div>
            <div class="tl">
              <div v-for="(r, i) in timeline" :key="i" class="seg-r" :class="{ hl: hoverCode === r.code, sel: chosen.has(r.code) }"
                :style="{ width: `max(2px, ${r.pct}%)`, background: stepColor(r.code) }"
                :title="`${r.code} ${r.name}${r.times > 1 ? `（第 ${r.nth}/${r.times} 次）` : ''} · ${fmtDuration(r.seconds)}`"
                @mouseenter="hoverCode = r.code" @mouseleave="hoverCode = ''" @click="toggleStep(r.code)"></div>
            </div>
          </div>

          <div class="sec">
            <div class="sec-h">工步
              <span class="muted">时长为每次停留的总和（按心跳时间）</span>
              <div class="quick">
                <button class="link" @click="selectSteps(() => true)">全选</button>
                <button class="link" @click="selectSteps((s) => s.code === '13.0')">只选等径</button>
                <button class="link" @click="selectSteps((s) => !s.sub)">只选主工步</button>
                <button class="link" @click="chosen = new Set()">清空</button>
              </div>
            </div>
            <div class="steps">
              <label v-for="s in stats.steps" :key="s.code" class="sr" :class="{ on: chosen.has(s.code), hl: hoverCode === s.code }"
                @mouseenter="hoverCode = s.code" @mouseleave="hoverCode = ''">
                <input type="checkbox" :checked="chosen.has(s.code)" @change="toggleStep(s.code)" />
                <span class="sw" :style="{ background: stepColor(s.code) }"></span>
                <span class="code num">{{ s.code }}</span>
                <span class="nm">{{ s.name }}<span v-if="s.sub" class="tag">子工步</span></span>
                <span class="bar"><i :style="{ width: (s.seconds / maxStep) * 100 + '%', background: stepColor(s.code) }"></i></span>
                <span class="dur num">{{ fmtDuration(s.seconds) }}</span>
                <span class="vis num" :class="{ multi: s.visits > 1 }" :title="s.visits > 1 ? `来回进入了 ${s.visits} 次` : '进入 1 次'">×{{ s.visits }}</span>
                <span class="rows num muted">{{ s.rows.toLocaleString() }} 行</span>
              </label>
            </div>
          </div>
        </template>

        <div class="exp">
          <div class="cols">
            <span class="k">导出点位</span>
            <label><input v-model="colMode" type="radio" value="all" /> 全部（{{ allCols.length }}）</label>
            <label><input v-model="colMode" type="radio" value="custom" /> 自选</label>
            <button v-if="colMode === 'custom'" class="link" @click="picking = !picking">{{ picking ? '收起' : '选择' }}（{{ exportCols.length }}）</button>
          </div>
          <TagPicker v-if="picking && colMode === 'custom'" :model-value="custom" :options="allCols" :max="allCols.length"
            @update:model-value="setCustom" @close="picking = false" />
          <div class="acts">
            <span class="muted">表头为“中文名 [点位]”；已勾选 {{ chosen.size }} 个工步，约 {{ chosenRows.toLocaleString() }} 行</span>
            <button class="btn" :disabled="!chosen.size" @click="exportCSV(true)">导出所选工步</button>
            <button class="btn primary" @click="exportCSV(false)">导出整棒 CSV</button>
          </div>
          <div v-if="error" class="err">{{ error }}</div>
        </div>
      </div>
      <div v-else class="card empty">
        <div class="big">{{ loading ? '读取中…' : '选择左边的一根晶棒' }}</div>
      </div>
    </div>
  </template>
</template>

<style scoped>
.cg { display: grid; grid-template-columns: 280px minmax(0, 1fr); gap: 14px; align-items: start }
.hd { flex-wrap: wrap; gap: 8px }
.cnt { font-weight: 500; font-size: 12px }
.seg { display: inline-flex; gap: 2px; background: #f3f5fb; border-radius: 9px; padding: 2px; margin-left: auto }
.seg button { border: 0; background: none; padding: 3px 8px; border-radius: 7px; font-weight: 600; font-size: 12px; color: var(--muted); cursor: pointer }
.seg button.on { background: #fff; color: var(--brand); box-shadow: 0 1px 2px rgba(15, 23, 42, .1) }
.btn.sm { padding: 3px 9px; font-size: 12px }
.btn:disabled { opacity: .5; cursor: default }

.items { display: grid; gap: 6px; padding: 10px; max-height: calc(100vh - 250px); overflow-y: auto }
.ci { text-align: left; border: 1px solid var(--line); background: #fff; border-radius: 12px; padding: 9px 12px; cursor: pointer;
  transition: border-color .15s, box-shadow .15s, background .15s }
.ci:hover { border-color: #c7d2fe }
.ci.on { border-color: var(--brand); background: var(--brand-soft); box-shadow: 0 0 0 3px rgba(99, 102, 241, .12) }
.ci-top { display: flex; align-items: center; gap: 6px; font-size: 12px; white-space: nowrap }
.dot { width: 7px; height: 7px; border-radius: 50% }
.dot.live { background: var(--ok); box-shadow: 0 0 0 3px rgba(16, 185, 129, .18) }
.dot.done { background: #cbd5e1 }
.st { font-weight: 700; color: #334155 }
.no { margin-left: auto; font-size: 11px }
.ci-dur { font-size: 20px; font-weight: 800; margin: 2px 0 1px; color: var(--ink) }
.ci-meta { display: flex; justify-content: space-between; font-size: 11.5px }
.chip.xs { font-size: 11px; padding: 0 7px }
.mono { font-family: ui-monospace, Consolas, monospace }
.empty.sm { padding: 24px 12px; font-size: 12.5px; text-align: center }

.detail { padding: 14px 18px }
.dh { display: flex; gap: 16px; align-items: center; flex-wrap: wrap; padding-bottom: 12px; border-bottom: 1px solid var(--line2) }
.title { font-size: 16px; font-weight: 800; display: flex; align-items: center; gap: 8px }
.title .mono { font-size: 14px; color: var(--muted) }
.sub { font-size: 11.5px; margin-top: 2px }
.tiles { display: flex; gap: 8px; margin-left: auto; flex-wrap: wrap }
.tile { border: 1px solid var(--line); border-radius: 11px; padding: 6px 12px; min-width: 86px }
.tile .k { font-size: 11px; color: var(--muted); font-weight: 600 }
.tile .v { font-size: 17px; font-weight: 800 }
.tile .v small { font-size: 11px; font-weight: 600; color: var(--muted) }
.tile.warn { border-color: #f3d9ac; background: var(--warn-soft) }
.tile.warn .v { color: var(--warn) }

.marks { display: flex; flex-wrap: wrap; gap: 6px 18px; padding: 10px 0 2px; font-size: 12.5px }
.mk { display: flex; gap: 6px; align-items: baseline }
.mk .k { color: var(--muted); font-weight: 600 }
.mk .v { font-weight: 700; color: #334155 }
.sec { margin-top: 14px }
.sec-h { display: flex; align-items: center; gap: 8px; font-weight: 700; font-size: 13px; margin-bottom: 8px }
.sec-h .muted { font-weight: 500; font-size: 11.5px }
.quick { margin-left: auto; display: flex; gap: 10px }
.link { border: 0; background: none; color: var(--brand); cursor: pointer; font-weight: 600; font-size: 12px; padding: 0 }
.tl { display: flex; height: 26px; border-radius: 8px; overflow: hidden; background: #f1f3f9; gap: 1px }
.seg-r { height: 100%; flex: none; cursor: pointer; opacity: .78; transition: opacity .12s, transform .12s }
.seg-r.hl, .seg-r:hover { opacity: 1; transform: scaleY(1.12) }
.seg-r.sel { opacity: 1; box-shadow: inset 0 -4px 0 rgba(15, 23, 42, .45) }

.steps { display: grid; gap: 2px }
.sr { display: grid; grid-template-columns: 18px 10px 42px minmax(110px, 1.2fr) minmax(80px, 2fr) 74px 38px 82px; align-items: center; gap: 10px;
  padding: 6px 10px; border-radius: 9px; cursor: pointer; font-size: 13px }
.sr:hover, .sr.hl { background: #f6f7fc }
.sr.on { background: var(--brand-soft) }
.sw { width: 10px; height: 10px; border-radius: 3px }
.code { color: var(--muted); font-size: 12px }
.nm { font-weight: 700; color: #334155; white-space: nowrap; overflow: hidden; text-overflow: ellipsis }
.tag { margin-left: 6px; font-size: 10.5px; font-weight: 600; color: var(--muted); background: #eef1f6; border-radius: 5px; padding: 0 5px }
.bar { height: 6px; background: #eef1f7; border-radius: 3px; overflow: hidden }
.bar i { display: block; height: 100%; border-radius: 3px; opacity: .8 }
.dur { font-weight: 800; text-align: right }
.vis { font-size: 12px; color: var(--faint); text-align: center }
.vis.multi { color: var(--warn); font-weight: 800; background: var(--warn-soft); border-radius: 6px }
.rows { font-size: 11.5px; text-align: right }

.exp { margin-top: 14px; padding-top: 12px; border-top: 1px solid var(--line2) }
.cols { display: flex; gap: 14px; align-items: center; flex-wrap: wrap; font-size: 13px; margin-bottom: 8px }
.cols .k { font-weight: 700 }
.cols label { display: flex; align-items: center; gap: 5px; cursor: pointer }
.acts { display: flex; align-items: center; gap: 8px; justify-content: flex-end; flex-wrap: wrap }
.acts .muted { margin-right: auto; font-size: 12px }
.err { color: var(--danger); font-weight: 600; font-size: 13px; margin-top: 8px }
.err.pad { margin: 0; padding: 10px 14px 0 }
@media (max-width: 1100px) { .cg { grid-template-columns: 1fr } .items { max-height: 320px } }
</style>
