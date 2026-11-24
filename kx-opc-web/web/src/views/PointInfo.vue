<script setup>
// 配置信息 → 点位信息（内部账户）：每个点位（opc_data 的列名，就是 OPC 的 tag）对应的中文名和 DB 点位（PLC 地址），全站显示和导出表头都用它。
// 数据在 kx_opc.opc_point_info 表里，页面只读；修改有两种方式：
//   1. 下载 CSV，在 Excel 里改好后上传（只修改文件里出现的点位）
//   2. 直接在数据库里 INSERT（每个点位以最新一行为准，一分钟内生效）
import { ref, computed } from 'vue'
import { state } from '../store.js'

const filter = ref('')
const onlyMissing = ref(false)
const rows = computed(() => {
  const q = filter.value.trim().toLowerCase()
  return state.columns
    .map((c) => ({ point: c.name, type: c.type, ...(state.points[c.name] || {}) }))
    .filter((r) => !onlyMissing.value || !r.name)
    .filter((r) => !q || [r.point, r.name, r.db_point].some((v) => (v || '').toLowerCase().includes(q)))
})
const configured = computed(() => state.columns.filter((c) => state.points[c.name]?.name).length)

const msg = ref(null) // { ok, text }
const uploading = ref(false)
const fileInput = ref(null)
function pick() { fileInput.value.click() }
async function upload(e) {
  const file = e.target.files[0]
  e.target.value = ''
  if (!file) return
  uploading.value = true
  msg.value = null
  try {
    // 原样上传字节，后端识别 UTF-8 / GBK（Excel 另存的 CSV 一般是 GBK）
    const r = await fetch('/api/points/import', {
      method: 'POST',
      headers: { 'Content-Type': 'text/csv' },
      body: await file.arrayBuffer(),
    })
    const res = await r.json().catch(() => ({}))
    if (!r.ok) throw new Error(res.error || r.statusText)
    state.points = res.points
    msg.value = { ok: true, text: `已读取 ${res.rows} 个点位，修改了 ${res.changed} 个` }
  } catch (err) {
    msg.value = { ok: false, text: '上传失败：' + err.message }
  } finally {
    uploading.value = false
  }
}

const table = computed(() => (state.pointTable || 'kx_opc.opc_point_info').replace(/`/g, ''))
const sqlExample = computed(() =>
  `INSERT INTO ${table.value} (ts, point, name, db_point)\n  VALUES (NOW, 'MELT_TEMP_PV', '熔体温度', '33.124-0');`)
</script>

<template>
  <div class="card intro">
    <div class="row">
      <div class="txt">
        <b>点位配置</b>：每个点位（<code>opc_data</code> 的列名，就是 OPC 的 tag）对应的中文名和 DB 点位，查看数据、曲线、导出表头都用它；没配置的直接显示点位名。
        已配置 <b class="num">{{ configured }}</b> / {{ state.columns.length }} 个点位。
      </div>
      <div class="acts">
        <a class="btn" href="/api/points/export">下载 CSV</a>
        <button class="btn primary" :disabled="uploading" @click="pick">{{ uploading ? '上传中…' : '上传 CSV' }}</button>
        <input ref="fileInput" type="file" accept=".csv,text/csv" hidden @change="upload" />
      </div>
    </div>
    <div v-if="msg" class="msg" :class="{ bad: !msg.ok }">{{ msg.text }}</div>
    <details class="how">
      <summary>怎么修改</summary>
      <ol>
        <li><b>上传 CSV</b>：点“下载 CSV”得到全部点位，在 Excel 里填中文名、DB 点位，保存后上传。表头为 <code>点位,中文名,DB点位</code>，
          列的顺序不限、可以只有其中两列（没有的列不改）；UTF-8 和 Excel 默认的 GBK 编码都可以。只修改文件里出现的点位。</li>
        <li><b>直接写数据库</b>：表 <code>{{ table }}</code>，每个点位以最新一行为准，网站一分钟内读到：
          <pre>{{ sqlExample }}</pre></li>
      </ol>
    </details>
  </div>

  <div class="card">
    <div class="hd">
      <input v-model="filter" class="input f" placeholder="点位 / 中文名 / DB 点位" />
      <label class="only"><input v-model="onlyMissing" type="checkbox" /> 只看没有中文名的</label>
      <span class="muted cnt">{{ rows.length }} 个</span>
    </div>
    <div class="wrap">
      <table class="grid">
        <thead><tr><th>点位（OPC tag）</th><th>类型</th><th>中文名</th><th>DB 点位</th></tr></thead>
        <tbody>
          <tr v-for="r in rows" :key="r.point">
            <td class="mono">{{ r.point }}</td>
            <td class="muted">{{ r.type }}</td>
            <td><b v-if="r.name">{{ r.name }}</b><span v-else class="none">未配置</span></td>
            <td class="mono">{{ r.db_point || '' }}</td>
          </tr>
          <tr v-if="!rows.length"><td colspan="4" class="empty">没有符合条件的点位</td></tr>
        </tbody>
      </table>
    </div>
  </div>
</template>

<style scoped>
.intro { padding: 12px 16px; margin-bottom: 12px; font-size: 13px }
.row { display: flex; align-items: center; gap: 14px; flex-wrap: wrap }
.txt { flex: 1; min-width: 300px; line-height: 1.7 }
.acts { display: flex; gap: 8px; align-items: center }
a.btn { text-decoration: none }
.msg { margin-top: 8px; font-weight: 600; color: var(--ok) }
.msg.bad { color: var(--danger) }
.how { margin-top: 8px; color: var(--muted) }
.how summary { cursor: pointer; font-weight: 600 }
.how ol { margin: 6px 0 0; padding-left: 20px; line-height: 1.8 }
.how pre { background: #f6f7fb; border: 1px solid var(--line); border-radius: 8px; padding: 8px 10px; margin: 6px 0; font-size: 12px; white-space: pre-wrap }
code { background: #f1f3f9; border-radius: 5px; padding: 0 5px; font-size: 12px }
.hd { gap: 10px }
.f { width: 260px; padding: 6px 10px }
.only { display: flex; align-items: center; gap: 5px; font-weight: 600; font-size: 12.5px }
.cnt { margin-left: auto; font-size: 12.5px }
.wrap { overflow: auto; max-height: calc(100vh - 300px) }
.mono { font-family: ui-monospace, Consolas, monospace; font-size: 12px }
.none { color: var(--faint); font-size: 12px }
</style>
