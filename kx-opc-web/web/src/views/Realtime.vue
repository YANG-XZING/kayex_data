<script setup>
// 炉台实时数据：表格显示，每秒刷新，变化的值闪一下。
// 中文名、单位来自点位信息（kx_opc.opc_point_info）；星标的点位排在最前。
// 宽屏时表格自动分成 2~3 栏并排，一屏看完全部点位。
import { ref, computed, onMounted, onBeforeUnmount } from 'vue'
import Icon from '../components/Icon.vue'
import { state, currentFurnace, isCustomer, labelOf, fmtField, save } from '../store.js'

const filter = ref('')
const tab = ref('all')
const pinned = ref(new Set(JSON.parse(localStorage.getItem('kx.pinned') || '[]')))

function togglePin(name) {
  const s = new Set(pinned.value)
  s.has(name) ? s.delete(name) : s.add(name)
  pinned.value = s
  save('kx.pinned', [...s])
}

const kindOf = (c) => (c.numeric ? 'num' : c.type === 'BOOL' ? 'bool' : 'text')
const TYPE_TEXT = { num: '数值', bool: '开关', text: '文本' }
const TABS = [
  { key: 'all', label: '全部' },
  { key: 'pinned', label: '关注' },
  { key: 'num', label: '数值' },
  { key: 'bool', label: '开关量' },
  { key: 'text', label: '文本' },
]
const counts = computed(() => {
  const n = { all: state.columns.length, pinned: 0, num: 0, bool: 0, text: 0 }
  for (const c of state.columns) {
    n[kindOf(c)]++
    if (pinned.value.has(c.name)) n.pinned++
  }
  return n
})

const rows = computed(() => {
  const q = filter.value.trim().toLowerCase()
  const match = (c) => !q || c.name.toLowerCase().includes(q) || labelOf(c.name).toLowerCase().includes(q)
  const list = state.columns.filter((c) => {
    if (!match(c)) return false
    if (tab.value === 'pinned') return pinned.value.has(c.name)
    if (tab.value !== 'all') return kindOf(c) === tab.value
    return true
  })
  // 关注的排在最前，其余保持数据库列顺序
  return [...list.filter((c) => pinned.value.has(c.name)), ...list.filter((c) => !pinned.value.has(c.name))]
})

// 按容器宽度分栏，按列优先排布（先排满第一栏）
const box = ref(null)
const width = ref(1200)
let ro = null
// 分栏数：0 为按宽度自动，也可固定 1~3 栏（文本长时选 1 栏，值列更宽）
const colsMode = ref(Number(localStorage.getItem('kx.rtCols')) || 0)
function setCols(n) { colsMode.value = n; save('kx.rtCols', n) }
const blocks = computed(() => {
  const k = colsMode.value || (width.value >= 1500 ? 3 : width.value >= 980 ? 2 : 1)
  const per = Math.ceil(rows.value.length / k) || 1
  const out = []
  for (let i = 0; i < rows.value.length; i += per) out.push(rows.value.slice(i, i + per))
  return out
})

// “当前值”列宽（占表格的百分比）：拖动表头左边的竖线调整，双击恢复默认；记在浏览器里
const VAL_DEFAULT = 34
const valPct = ref(Number(localStorage.getItem('kx.rtValW')) || VAL_DEFAULT)
function startResize(e) {
  const tableWidth = e.target.closest('table').clientWidth
  const x0 = e.clientX
  const p0 = valPct.value
  const move = (ev) => { valPct.value = Math.min(70, Math.max(15, p0 + ((x0 - ev.clientX) / tableWidth) * 100)) }
  const up = () => {
    window.removeEventListener('mousemove', move)
    window.removeEventListener('mouseup', up)
    document.body.style.cursor = ''
    save('kx.rtValW', Math.round(valPct.value))
  }
  document.body.style.cursor = 'col-resize'
  window.addEventListener('mousemove', move)
  window.addEventListener('mouseup', up)
}
function resetResize() { valPct.value = VAL_DEFAULT; save('kx.rtValW', VAL_DEFAULT) }

onMounted(() => {
  ro = new ResizeObserver(() => { if (box.value) width.value = box.value.clientWidth })
  if (box.value) ro.observe(box.value)
})
onBeforeUnmount(() => ro?.disconnect())
// 表格在拿到数据后才渲染，此时再开始观察宽度
function setBox(el) {
  box.value = el
  if (el && ro) {
    ro.observe(el)
    width.value = el.clientWidth
  }
}

const hasData = computed(() => state.live.table === state.current && state.live.ts > 0)
</script>

<template>
  <div class="card">
      <div class="hd bar">
        <div class="tabs">
          <button v-for="t in TABS" :key="t.key" class="tab" :class="{ on: tab === t.key }" @click="tab = t.key">
            {{ t.label }}<span class="n">{{ counts[t.key] }}</span>
          </button>
        </div>
        <div class="cols">
          <span class="muted">分栏</span>
          <button v-for="n in [0, 1, 2, 3]" :key="n" :class="{ on: colsMode === n }" @click="setCols(n)">{{ n ? n : '自动' }}</button>
        </div>
        <input v-model="filter" class="input filter" placeholder="筛选点位名或中文名" />
      </div>

      <div v-if="!hasData" class="empty">
        <div class="big">等待数据…</div>
        <div>{{ currentFurnace.status === 'online' ? '正在读取最新一行' : '这台炉台目前没有新数据' }}</div>
      </div>
      <div v-else-if="!rows.length" class="empty">
        <template v-if="!state.columns.length && isCustomer">
          <div class="big">还没有可查看的点位</div>
          <div>请联系管理员在“配置信息 → 客户点位配置”里勾选</div>
        </template>
        <template v-else>
          <div class="big">没有符合条件的点位</div>
          <div>{{ tab === 'pinned' ? '点表格里的星标把常看的点位加入关注' : '换个筛选条件试试' }}</div>
        </template>
      </div>
      <div v-else :ref="setBox" class="blocks" :style="{ gridTemplateColumns: `repeat(${blocks.length}, minmax(0, 1fr))` }">
        <table v-for="(blk, bi) in blocks" :key="bi" class="grid rt">
          <colgroup><col style="width: 30px" /><col /><col :style="{ width: valPct + '%' }" /><col style="width: 58px" /></colgroup>
          <thead>
            <tr><th></th><th>点位</th>
              <th class="r rs">当前值<span class="grip" title="拖动调整“当前值”列宽，双击恢复" @mousedown.prevent="startResize" @dblclick="resetResize"></span></th>
              <th>类型</th></tr>
          </thead>
          <tbody>
            <tr v-for="c in blk" :key="c.name" :class="{ pinned: pinned.has(c.name) }">
              <td>
                <button class="pin" :class="{ on: pinned.has(c.name) }" :title="pinned.has(c.name) ? '取消关注' : '关注'"
                  @click="togglePin(c.name)"><Icon name="star" :size="14" /></button>
              </td>
              <td class="name" :title="`${c.name}（${c.type}）`">
                {{ labelOf(c.name) }}<span v-if="labelOf(c.name) !== c.name" class="pt">{{ c.name }}</span>
              </td>
              <td class="r val">
                <span :key="state.live.changedAt[c.name] || 0" class="v num" :class="{ flash: state.live.changedAt[c.name] }"
                  :title="String(state.live.values[c.name] ?? '')">
                  <span v-if="kindOf(c) === 'bool'" class="chip" :class="state.live.values[c.name] ? 'ok' : 'mute'">
                    {{ fmtField(c.name, state.live.values[c.name]) }}</span>
                  <template v-else>{{ fmtField(c.name, state.live.values[c.name]) }}</template>
                </span>
              </td>
              <td><span class="type" :class="kindOf(c)">{{ TYPE_TEXT[kindOf(c)] }}</span></td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>
</template>

<style scoped>
.bar { flex-wrap: wrap; font-weight: 600 }
.tabs { display: flex; gap: 4px; background: #f3f5fb; padding: 3px; border-radius: 10px }
.tab { border: 0; background: none; padding: 5px 12px; border-radius: 8px; font-weight: 600; font-size: 12.5px; color: var(--muted); cursor: pointer }
.tab .n { margin-left: 5px; font-size: 11px; color: var(--faint); font-variant-numeric: tabular-nums }
.tab.on { background: #fff; color: var(--brand); box-shadow: 0 1px 3px rgba(15, 23, 42, .1) }
.tab.on .n { color: var(--brand2) }
.filter { width: 220px; margin-left: auto; padding: 6px 10px }

.blocks { display: grid; gap: 0 }
.blocks > table + table { border-left: 1px solid var(--line) }
table.rt { table-layout: fixed }
table.rt th { top: 0; font-size: 12px; padding: 8px 10px }
table.rt td { padding: 6px 10px; height: 35px }
table.rt th.r, table.rt td.r { text-align: right }
table.rt tbody tr:nth-child(even) td { background: #fafbfe }
table.rt tbody tr.pinned td { background: #fffbeb }
table.rt tbody tr:hover td { background: #eef2ff }
/* 点位名和值都完整显示：放不下就换行，不再用省略号截断 */
.name { font-weight: 600; color: #334155; word-break: break-word }
.name .pt { margin-left: 6px; font-size: 11px; font-weight: 500; color: var(--faint) }
.v { display: inline-block; max-width: 100%; vertical-align: middle; white-space: normal; word-break: break-all; text-align: right;
  font-weight: 700; font-size: 14px; padding: 1px 6px; border-radius: 6px }
/* 当前值列宽拖动条：表头左边缘 */
th.rs { position: relative }
.grip { position: absolute; left: -4px; top: 0; bottom: 0; width: 8px; cursor: col-resize; z-index: 1 }
.grip::after { content: ''; position: absolute; left: 3px; top: 25%; bottom: 25%; width: 2px; border-radius: 1px; background: #cbd5e1 }
.grip:hover::after { background: var(--brand) }
.cols { display: inline-flex; align-items: center; gap: 2px; margin-left: 12px; font-size: 12px }
.cols .muted { margin-right: 4px }
.cols button { border: 1px solid var(--line); background: #fff; border-radius: 7px; padding: 3px 9px; font-size: 12px; font-weight: 600;
  color: var(--muted); cursor: pointer }
.cols button.on { background: var(--brand-soft); border-color: var(--brand); color: var(--brand) }
.type { font-size: 11px; font-weight: 700; padding: 1px 7px; border-radius: 6px }
.type.num { background: var(--brand-soft); color: var(--brand-ink) }
.type.bool { background: var(--ok-soft); color: var(--ok) }
.type.text { background: #f1f5f9; color: var(--muted) }
.pin { border: 0; background: none; padding: 0; color: #cbd5e1; cursor: pointer; display: grid }
.pin:hover { color: var(--faint) }
.pin.on { color: #f59e0b }
.pin.on :deep(path) { fill: currentColor }
.flash { animation: flash 1.2s ease-out }
@keyframes flash { from { background: #c7d2fe } to { background: transparent } }
</style>
