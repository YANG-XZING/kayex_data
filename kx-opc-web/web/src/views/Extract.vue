<script setup>
// 全量数据导出（数据来自 kayex_data，和 OPC 监控的 kx_opc 是两套数据），原 TDengine 取数网站的两个模式，导出文件格式与原网站一致：
// 查询区间就是页面上选的开始～结束（原网站会把结束时间自动往后延 1 天，这里不再延）。
//   PLC / CCD 取数：按设备、时间、点位取 PLC 数据，可按采集时间关联 CCD，按工步分段；
//                  可导出全量 CSV、分炉次 / 晶棒 CSV、单段或全部分段（zip）
//   硬件信息：查询工控机的硬件信息表
// 设备清单来自 kayex_data 的标签；点位表、分段设置记在本机浏览器里。
import { ref, reactive, computed, watch } from 'vue'
import { useRoute } from 'vue-router'
import DevicePicker from '../components/DevicePicker.vue'
import VirtualTable from '../components/VirtualTable.vue'
import { getJSON, save, fmtDuration } from '../store.js'

const route = useRoute()
const hardware = computed(() => route.path === '/hardware')

const load = (k, f) => { try { return JSON.parse(localStorage.getItem(k)) ?? f } catch { return f } }
const p2 = (n) => String(n).padStart(2, '0')
const toLocal = (d) => `${d.getFullYear()}-${p2(d.getMonth() + 1)}-${p2(d.getDate())}T${p2(d.getHours())}:${p2(d.getMinutes())}`
const now0 = () => { const d = new Date(); d.setSeconds(0, 0); return d }

const form = reactive({
  device: load('kx.extract.device', ''),
  start: toLocal(new Date(now0().getTime() - 24 * 3600 * 1000)),
  end: toLocal(now0()),
  includeCcd: load('kx.extract.ccdOn', true),
  plc: [],
  ccd: [],
  startStep: load('kx.extract.startStep', '13'),
  allowed: load('kx.extract.allowed', '13,0'),
  minSeg: load('kx.extract.minSeg', 5),
  joinMode: load('kx.extract.joinMode', 'exact'),
  asofTol: load('kx.extract.asofTol', 1),
})
for (const [k, key] of [['device', 'device'], ['plc', 'plc'], ['ccd', 'ccd'], ['includeCcd', 'ccdOn'],
  ['startStep', 'startStep'], ['allowed', 'allowed'], ['minSeg', 'minSeg'], ['joinMode', 'joinMode'], ['asofTol', 'asofTol']]) {
  watch(() => form[k], (v) => save('kx.extract.' + key, v), { deep: true })
}

// 时间快捷：按整天或最近 N 小时
const QUICK = [
  { label: '今天', f: () => { const s = now0(); s.setHours(0, 0); return [s, now0()] } },
  { label: '昨天', f: () => { const e = now0(); e.setHours(0, 0); return [new Date(e.getTime() - 86400000), e] } },
  { label: '最近 24 小时', f: () => [new Date(now0().getTime() - 86400000), now0()] },
  { label: '最近 3 天', f: () => [new Date(now0().getTime() - 3 * 86400000), now0()] },
  { label: '最近 7 天', f: () => [new Date(now0().getTime() - 7 * 86400000), now0()] },
]
function quick(q) {
  const [s, e] = q.f()
  form.start = toLocal(s)
  form.end = toLocal(e)
}

// ---------- 默认配置、设备清单 ----------
const defaults = ref(null)
const devices = ref([])
const devicesLoading = ref(true)
const error = ref('')
getJSON('/api/extract/defaults').then((d) => {
  defaults.value = d
  form.plc = load('kx.extract.plc', null) || d.plc_points.map((x) => ({ ...x }))
  form.ccd = load('kx.extract.ccd', null) || d.ccd_points.map((x) => ({ ...x }))
}).catch((e) => { error.value = '读取默认配置失败：' + e.message })
getJSON('/api/extract/devices').then((d) => { devices.value = d })
  .catch((e) => { error.value = '读取 kayex_data 设备清单失败（仍可直接输入 IP）：' + e.message })
  .finally(() => { devicesLoading.value = false })

function resetPoints() {
  if (!defaults.value) return
  form.plc = defaults.value.plc_points.map((x) => ({ ...x }))
  form.ccd = defaults.value.ccd_points.map((x) => ({ ...x }))
}
const allowedList = computed(() => String(form.allowed).replace(/，/g, ',').split(',').map((x) => x.trim()).filter(Boolean))
function toggleStep(s) {
  const a = allowedList.value
  form.allowed = (a.includes(s) ? a.filter((x) => x !== s) : [...a, s]).join(',')
}
const stepLabel = (s) => defaults.value?.step_labels?.find((x) => x.step === String(s))?.label
const plcOn = computed(() => form.plc.filter((x) => x.on && x.point.trim()))
const ccdOn = computed(() => form.ccd.filter((x) => x.on && x.point.trim()))
const editing = ref('') // '' / 'points' / 'segment'
const hasStepPoint = computed(() => plcOn.value.some((x) => x.point.trim() === '33.0-0'))

// ---------- 取数方式：全量（按时间）/ 按晶棒 ----------
// 按晶棒用 kayex_data 自己的晶棒台账（编号和 OPC 那边不是同一套），查询区间就是这根晶在台账里的起止 ts
// 由左侧菜单选（全量数据 / 数据管理 / 导出数据 下的“全量导出”“按晶棒导出”），和 OPC 数据的导出菜单一致
const mode = computed(() => (route.meta.extract === 'crystal' ? 'crystal' : 'time'))
const cDays = ref(load('kx.extract.cDays', 30))
watch(cDays, (d) => save('kx.extract.cDays', d))
const crystals = ref([])
const crystalsNow = ref(0)
const crystalsLoading = ref(false)
const crystal = ref(null)
async function loadCrystals() {
  crystals.value = []
  crystal.value = null
  if (!form.device || hardware.value || mode.value !== 'crystal') return
  crystalsLoading.value = true
  try {
    const d = await getJSON(`/api/extract/crystals?device=${encodeURIComponent(form.device)}&days=${cDays.value}`)
    crystals.value = d.crystals
    crystalsNow.value = d.now
    crystal.value = d.crystals[0] || null
  } catch (e) {
    error.value = '读取晶棒台账失败：' + e.message
  } finally {
    crystalsLoading.value = false
  }
}
watch([() => form.device, mode, cDays, hardware], loadCrystals, { immediate: true })
const short = (no) => (no ? no.slice(0, 8) : '—')
const cSeconds = (c) => (c.end ? c.seconds : Math.max(0, (crystalsNow.value - c.start) / 1000))
const md = (s) => (s ? s.slice(5, 16) : '')

// ---------- 查询 ----------
const loading = ref(false)
const result = ref(null) // 接口返回
const view = ref(0) // 0 全部，n 第 n 段
const viewData = ref(null) // { columns, rows, total, shown, capped }
const elapsed = ref(0)

async function post(url, body) {
  const r = await fetch(url, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) })
  const js = await r.json().catch(() => ({}))
  if (!js.ok) throw new Error(js.error || r.statusText)
  return js
}
async function run() {
  error.value = ''
  if (!form.device) { error.value = '请先选择设备'; return }
  const byCrystal = !hardware.value && mode.value === 'crystal'
  if (byCrystal && !crystal.value) { error.value = '请先选择晶棒'; return }
  loading.value = true
  const t0 = performance.now()
  const body = { device: form.device.trim(), start: form.start.replace('T', ' '), end: form.end.replace('T', ' ') }
  if (byCrystal) {
    body.start_ms = crystal.value.start
    body.end_ms = crystal.value.end ?? Date.now() + 86400000 // 进行中：取到最新
  }
  try {
    if (hardware.value) {
      result.value = await post('/api/extract/hardware', body)
    } else {
      result.value = await post('/api/extract/query', {
        ...body, end_plus_day: false, include_ccd: form.includeCcd,
        plc_points: form.plc, ccd_points: form.ccd, join_mode: form.joinMode, asof_tol: form.asofTol,
        min_seg_rows: form.minSeg, start_step: form.startStep, allowed_steps: form.allowed,
      })
    }
    elapsed.value = (performance.now() - t0) / 1000
    editing.value = ''
    showAll()
  } catch (e) {
    error.value = '查询失败：' + e.message
  } finally {
    loading.value = false
  }
}
function showAll() {
  view.value = 0
  viewData.value = result.value
}
async function showSegment(i) {
  view.value = i
  try {
    viewData.value = await getJSON(`/api/extract/segment_rows?token=${result.value.token}&index=${i}`)
  } catch (e) {
    error.value = '加载分段失败：' + e.message
  }
}
function download(path, extra = '') {
  location.href = `/api/extract/${path}?token=${result.value.token}${extra}`
}
const fmtCell = (v) => (v === null || v === undefined ? '·' : typeof v === 'boolean' ? (v ? 'True' : 'False') : String(v))
const tableCols = computed(() => (viewData.value?.columns || []).map((c, i) => ({ key: i, label: c })))
const cell = (r, k) => fmtCell(viewData.value.rows[r][k])
const deviceName = computed(() => devices.value.find((d) => d.ip === result.value?.device)?.id || '')

// 换页面（全量 / 按晶棒 / 硬件信息）时清掉结果
watch(() => route.path, () => { result.value = null; viewData.value = null; error.value = ''; editing.value = '' })
</script>

<template>
  <div class="card q">
    <div class="row">
      <span class="k">设备</span>
      <DevicePicker v-model="form.device" :devices="devices" :areas="defaults?.areas" :loading="devicesLoading" />
      <template v-if="hardware || mode === 'time'">
        <span class="k t2">时间</span>
        <input v-model="form.start" class="input t" type="datetime-local" />
        <span class="muted">至</span>
        <input v-model="form.end" class="input t" type="datetime-local" />
      </template>
      <template v-else-if="crystal">
        <span class="k t2">晶棒</span>
        <span class="chip brand mono">#{{ short(crystal.crystal_no) }}</span>
        <span class="muted">{{ md(crystal.start_capture) }} 起 · {{ crystal.end ? fmtDuration(cSeconds(crystal)) : '进行中' }}</span>
      </template>
      <button class="btn primary go" :disabled="loading" @click="run">{{ loading ? '查询中…' : '查询' }}</button>
    </div>
    <div v-if="hardware || mode === 'time'" class="row">
      <span class="k"></span>
      <button v-for="x in QUICK" :key="x.label" class="btn sm" @click="quick(x)">{{ x.label }}</button>
    </div>
    <div v-else class="crys">
      <div class="crys-h">
        <span>{{ form.device ? `这台设备最近 ${cDays} 天的晶棒` : '先选设备' }}</span>
        <span v-if="crystals.length" class="muted">{{ crystals.length }} 根</span>
        <div class="seg">
          <button v-for="d in [7, 30, 90]" :key="d" :class="{ on: cDays === d }" @click="cDays = d">{{ d }} 天</button>
        </div>
      </div>
      <div class="crys-list">
        <button v-for="c in crystals" :key="c.crystal_no" class="cc" :class="{ on: crystal?.crystal_no === c.crystal_no }" @click="crystal = c">
          <div class="cc-top">
            <span class="dot" :class="c.end ? 'done' : 'live'"></span>
            <span>{{ c.end ? '已结束' : '进行中' }}</span>
            <span v-if="c.is_first" class="chip brand xs">首棒</span>
            <span class="mono muted">#{{ short(c.crystal_no) }}</span>
          </div>
          <div class="cc-dur num">{{ fmtDuration(cSeconds(c)) }}</div>
          <div class="muted cc-meta">{{ md(c.start_capture) }} 起</div>
        </button>
        <div v-if="crystalsLoading" class="muted none">读取中…</div>
        <div v-else-if="form.device && !crystals.length" class="muted none">这段时间台账里没有这台设备的晶棒</div>
      </div>
    </div>

    <template v-if="!hardware">
      <div class="row sum">
        <span class="k">点位</span>
        <span class="chip brand">PLC {{ plcOn.length }} 个</span>
        <span class="chip" :class="form.includeCcd ? 'brand' : 'mute'">{{ form.includeCcd ? `CCD ${ccdOn.length} 个 · ${form.joinMode === 'exact' ? '按秒对齐' : `最近时刻 ±${form.asofTol} 秒`}` : '不关联 CCD' }}</span>
        <span v-if="!hasStepPoint" class="chip warn">没有启用 33.0-0（工步），不能分段</span>
        <button class="link" @click="editing = editing === 'points' ? '' : 'points'">{{ editing === 'points' ? '收起' : '修改点位' }}</button>
        <span class="k t2">分段</span>
        <span class="muted">从工步 <b>{{ form.startStep }}</b><template v-if="stepLabel(form.startStep)">（{{ stepLabel(form.startStep) }}）</template> 开始，
          连续 <b>{{ allowedList.join('、') }}</b> 不断段，最少 <b>{{ form.minSeg }}</b> 行</span>
        <button class="link" @click="editing = editing === 'segment' ? '' : 'segment'">{{ editing === 'segment' ? '收起' : '修改分段' }}</button>
      </div>

      <div v-if="editing === 'points'" class="panel">
        <div class="pcol">
          <div class="ph">PLC 点位（{{ defaults?.plc_stable }}）<button class="link" @click="resetPoints">恢复默认</button></div>
          <div v-for="(pt, i) in form.plc" :key="i" class="pt">
            <input v-model="pt.on" type="checkbox" title="启用" />
            <input v-model="pt.point" class="input sm" placeholder="33.24-0" />
            <input v-model="pt.name" class="input sm" placeholder="中文名" />
            <button class="x" title="删除" @click="form.plc.splice(i, 1)">×</button>
          </div>
          <button class="add" @click="form.plc.push({ point: '', name: '', on: true })">＋ 添加 PLC 点位</button>
        </div>
        <div class="pcol">
          <div class="ph">CCD 点位（{{ defaults?.ccd_stable }}）
            <label class="chk inline"><input v-model="form.includeCcd" type="checkbox" /> 关联 CCD</label></div>
          <template v-if="form.includeCcd">
            <div v-for="(pt, i) in form.ccd" :key="i" class="pt">
              <input v-model="pt.on" type="checkbox" title="启用" />
              <input v-model="pt.point" class="input sm" placeholder="BodyDM02R_003" />
              <input v-model="pt.name" class="input sm" placeholder="中文名" />
              <button class="x" title="删除" @click="form.ccd.splice(i, 1)">×</button>
            </div>
            <button class="add" @click="form.ccd.push({ point: '', name: '', on: true })">＋ 添加 CCD 点位</button>
            <div class="grid2">
              <label>关联方式
                <select v-model="form.joinMode" class="input sm"><option value="exact">按秒对齐</option><option value="asof">最近时刻</option></select></label>
              <label>最近时刻容差（秒）
                <input v-model.number="form.asofTol" class="input sm" type="number" min="1" :disabled="form.joinMode !== 'asof'" /></label>
            </div>
            <div class="hint">PLC 与 CCD 按采集时间（capture_time）关联。</div>
          </template>
        </div>
      </div>

      <div v-if="editing === 'segment'" class="panel seg1">
        <label>起始工步（每段从它开始）<input v-model="form.startStep" class="input sm" /></label>
        <label>分段最少行数（过滤噪声）<input v-model.number="form.minSeg" class="input sm" type="number" min="1" /></label>
        <div class="wide">连续包含的工步（属于这些就不断段）
          <div class="chips">
            <button v-for="s in defaults?.step_labels || []" :key="s.step" class="chipbtn" :class="{ on: allowedList.includes(s.step) }"
              @click="toggleStep(s.step)">{{ s.label }}（{{ s.step }}）</button>
            <input v-model="form.allowed" class="input sm al" placeholder="13,0" />
          </div>
        </div>
      </div>
    </template>
    <div v-if="error" class="err">{{ error }}</div>
  </div>

  <div v-if="!result" class="card empty">
    <div class="big">{{ hardware ? '选择设备和时间后点“查询”' : '选择设备、时间后点“查询”' }}</div>
    <div>数据来自 kayex_data{{ hardware ? `（${defaults?.hardware_table || '硬件信息表'}）` : '' }}；页面显示前 1000 行，导出为全量数据</div>
  </div>
  <template v-else>
    <div class="card res">
      <div class="top">
        <div class="stats">
          <div class="stat"><div class="k2">设备</div><div class="v">{{ deviceName || result.device }}<span class="muted ip num">{{ deviceName ? result.device : '' }}</span></div></div>
          <div class="stat"><div class="k2">总行数</div><div class="v num brand">{{ result.total.toLocaleString() }}</div></div>
          <div class="stat"><div class="k2">列数</div><div class="v num">{{ result.columns.length }}</div></div>
          <div v-if="!hardware" class="stat"><div class="k2">工步分段</div><div class="v num ok">{{ result.segments.length }}</div></div>
          <div class="stat"><div class="k2">耗时</div><div class="v num">{{ elapsed.toFixed(1) }} 秒</div></div>
        </div>
        <div class="exports">
          <button class="btn primary" @click="download('export')">导出全量 CSV</button>
          <button v-if="!hardware" class="btn" @click="download('export_split')">分炉次 / 晶棒 CSV</button>
          <button v-if="result.segments.length" class="btn" @click="download('export_all_segments')">全部分段（zip）</button>
        </div>
      </div>
      <div class="hint">实际查询区间：{{ result.q_start }} ~ {{ result.q_end }}
        <template v-if="result.step_col">　·　工步列：{{ result.step_col }}</template></div>

      <div v-if="result.segments.length" class="segs">
        <table class="grid">
          <thead><tr><th>分段</th><th class="r">行数</th><th>开始</th><th>结束</th><th></th></tr></thead>
          <tbody>
            <tr :class="{ on: view === 0 }" @click="showAll"><td><b>全部数据</b></td><td class="num r">{{ result.total.toLocaleString() }}</td><td colspan="2" class="muted">{{ result.q_start }} ~ {{ result.q_end }}</td><td></td></tr>
            <tr v-for="s in result.segments" :key="s.index" :class="{ on: view === s.index }" @click="showSegment(s.index)">
              <td>第 {{ s.index }} 段</td><td class="num r">{{ s.rows.toLocaleString() }}</td>
              <td class="num">{{ s.start }}</td><td class="num">{{ s.end }}</td>
              <td><button class="btn sm" @click.stop="download('export_segment', '&index=' + s.index)">下载</button></td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>

    <div v-if="viewData" class="preview">
      <div class="meta muted">
        {{ view ? `第 ${view} 段` : '全部数据' }} · 预览
        <template v-if="viewData.capped">前 {{ viewData.shown.toLocaleString() }} 行（共 {{ viewData.total.toLocaleString() }} 行，导出为完整数据）</template>
        <template v-else>{{ viewData.rows.length.toLocaleString() }} 行</template>
      </div>
      <VirtualTable :key="view + ':' + result.token" :columns="tableCols" :count="viewData.rows.length" :cell="cell" :height="520" />
    </div>
  </template>
</template>

<style scoped>
.q { padding: 12px 16px; margin-bottom: 12px }
.row { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; padding: 4px 0; font-size: 12.5px }
.k { font-weight: 700; font-size: 13px; width: 32px; flex: none }
.t2 { margin-left: 10px }
.t { width: 190px; padding: 6px 10px }
.go { margin-left: auto; padding: 8px 22px }
.btn.sm { padding: 4px 10px; font-size: 12.5px }
.btn:disabled { opacity: .6; cursor: wait }
.chk { display: flex; align-items: center; gap: 6px; font-weight: 600; cursor: pointer; margin-left: 8px }
.chk.inline { margin: 0 0 0 auto; font-weight: 500 }
.sum { border-top: 1px solid var(--line2); margin-top: 6px; padding-top: 10px }
.sum b { color: var(--ink) }
.link { border: 0; background: none; color: var(--brand); cursor: pointer; font-size: 12.5px; font-weight: 700 }
.panel { display: grid; grid-template-columns: 1fr 1fr; gap: 14px; margin-top: 8px; padding: 12px; border: 1px solid var(--line); border-radius: 12px; background: #fafbfe }
.ph { display: flex; align-items: center; gap: 8px; font-weight: 700; font-size: 13px; margin-bottom: 8px }
.ph .link { margin-left: auto }
.pt { display: grid; grid-template-columns: 18px 1fr 1fr 24px; gap: 6px; align-items: center; margin-bottom: 6px }
.input.sm { padding: 6px 8px; font-size: 12.5px }
.x { border: 0; background: none; color: var(--faint); font-size: 16px; cursor: pointer }
.x:hover { color: var(--danger) }
.add { width: 100%; border: 1px dashed #c9d2e3; background: #fff; color: var(--brand); font-weight: 600; border-radius: 10px; padding: 6px; cursor: pointer; font-size: 12.5px }
.add:hover { background: var(--brand-soft) }
.grid2 { display: grid; grid-template-columns: 1fr 1fr; gap: 8px; margin-top: 10px }
.grid2 label, .seg1 label, .seg1 .wide { display: grid; gap: 4px; font-size: 12px; color: var(--muted); font-weight: 600 }
.seg1 .wide { grid-column: 1 / -1 }
.chips { display: flex; flex-wrap: wrap; gap: 6px; align-items: center }
.chipbtn { border: 1px solid var(--line); background: #fff; border-radius: 999px; padding: 4px 10px; font-size: 12px; font-weight: 600; color: var(--muted); cursor: pointer }
.chipbtn.on { background: var(--brand); border-color: var(--brand); color: #fff }
.al { width: 140px }
.hint { font-size: 11.5px; color: var(--muted); margin-top: 6px }
.err { margin-top: 8px; color: var(--danger); font-weight: 600; font-size: 13px }

.res { padding: 12px 16px; margin-bottom: 12px }
.top { display: flex; justify-content: space-between; gap: 12px; flex-wrap: wrap; align-items: center }
.stats { display: flex; gap: 10px; flex-wrap: wrap }
.stat { border: 1px solid var(--line); border-radius: 12px; padding: 8px 14px; min-width: 88px }
.stat .k2 { font-size: 11px; color: var(--muted); font-weight: 600 }
.stat .v { font-size: 18px; font-weight: 800 }
.stat .ip { font-size: 12px; font-weight: 500; margin-left: 6px }
.v.brand { color: var(--brand) } .v.ok { color: var(--ok) }
.exports { display: flex; gap: 8px; flex-wrap: wrap }
.segs { margin-top: 10px; border: 1px solid var(--line); border-radius: 12px; overflow: auto; max-height: 260px }
.segs tbody tr { cursor: pointer }
.segs tbody tr.on td { background: var(--brand-soft) }
.r { text-align: right }
.preview .meta { font-size: 12.5px; margin: 0 4px 8px }
.mono { font-family: ui-monospace, Consolas, monospace }
/* 晶棒选择 */
.crys { margin-top: 4px }
.crys-h { display: flex; align-items: center; gap: 8px; font-size: 12.5px; font-weight: 700; margin: 4px 0 6px 40px }
.crys-h .muted { font-weight: 500 }
.seg { display: inline-flex; gap: 2px; background: #f3f5fb; border-radius: 9px; padding: 2px; margin-left: auto }
.seg button { border: 0; background: none; padding: 3px 9px; border-radius: 7px; font-weight: 600; font-size: 12px; color: var(--muted); cursor: pointer }
.seg button.on { background: #fff; color: var(--brand); box-shadow: 0 1px 2px rgba(15, 23, 42, .1) }
.crys-list { display: flex; gap: 8px; overflow-x: auto; padding: 2px 2px 8px 40px; scrollbar-width: thin }
.cc { flex: none; width: 168px; white-space: nowrap; text-align: left; border: 1px solid var(--line); background: #fff; border-radius: 11px; padding: 8px 11px; cursor: pointer }
.cc:hover { border-color: #c7d2fe }
.cc.on { border-color: var(--brand); background: var(--brand-soft); box-shadow: 0 0 0 3px rgba(99, 102, 241, .12) }
.cc-top { display: flex; align-items: center; gap: 5px; font-size: 11.5px; font-weight: 700; color: #334155 }
.cc-top .mono { margin-left: auto; font-weight: 500; font-size: 10.5px }
.cc-dur { font-size: 17px; font-weight: 800; margin-top: 2px }
.cc-meta { font-size: 11px }
.dot { width: 7px; height: 7px; border-radius: 50%; flex: none }
.dot.live { background: var(--ok); box-shadow: 0 0 0 3px rgba(16, 185, 129, .18) }
.dot.done { background: #cbd5e1 }
.chip.xs { font-size: 10.5px; padding: 0 6px }
.none { padding: 14px 4px; font-size: 12.5px }
@media (max-width: 1100px) { .panel { grid-template-columns: 1fr } }
</style>
