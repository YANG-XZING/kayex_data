<script setup>
// 历史数据：按 ts 查询，明确翻页；已选点位可拖动排序，表格和导出沿用此顺序。
import { ref, shallowRef, computed, watch } from 'vue'
import TagPicker from '../components/TagPicker.vue'
import SelectedTags from '../components/SelectedTags.vue'
import HistoryTable from '../components/HistoryTable.vue'
import { state, currentFurnace, save, load, toInput, fromInput, fmtDuration, serverNow, quickRanges } from '../store.js'

const maxTags = computed(() => state.settings.history_max_tags)
const maxRange = computed(() => state.settings.table_max_range_ms)
const allColumns = computed(() => state.columns.map((c) => c.name))
const quickList = computed(() => quickRanges(maxRange.value))
const end = ref(toInput(serverNow()))
const start = ref(toInput(serverNow() - 3600000))
function quick(ms) {
  const e = Math.floor(serverNow() / 1000) * 1000
  end.value = toInput(e)
  start.value = toInput(e - ms)
  query()
}

const saved = load('kx.histCols', [])
const selected = ref(Array.isArray(saved) ? saved : [])
watch(allColumns, (cols) => {
  if (!cols.length) return
  setSelected(selected.value.filter((c) => cols.includes(c)).slice(0, maxTags.value))
  if (!selected.value.length) setSelected(cols.slice(0, Math.min(4, maxTags.value)))
}, { immediate: true })
function setSelected(list) { selected.value = list; save('kx.histCols', list) }
const picking = ref(false)
const params = computed(() => {
  const from = fromInput(start.value)
  const to = fromInput(end.value)
  const cols = selected.value.filter((c) => allColumns.value.includes(c))
  let error = ''
  if (!currentFurnace.value) error = '请先选择炉台'
  else if (!cols.length) error = '请选择点位'
  else if (cols.length > maxTags.value) error = `最多 ${maxTags.value} 个点位`
  else if (!(from < to)) error = '开始时间必须早于结束时间'
  else if (to - from > maxRange.value) error = `时间范围不能超过 ${fmtDuration(maxRange.value / 1000)}`
  const qs = new URLSearchParams({ table: state.current, cols: cols.join(','), from, to })
  return { error, qs: qs.toString(), cols }
})
const error = ref('')
const loading = ref(false)
const result = shallowRef(null)
// 仅调整顺序时，已查到的数据按列名重排，不重复读取数据库。
const resultCols = computed(() => {
  const cols = result.value?.cols || []
  return selected.value.length === cols.length && selected.value.every((c) => cols.includes(c)) ? selected.value : cols
})
function query() {
  const p = params.value
  error.value = p.error
  if (p.error) return
  result.value = { cols: [...p.cols], qs: p.qs, title: `${currentFurnace.value.device_id} · ${start.value.replace('T', ' ')} 至 ${end.value.replace('T', ' ')}` }
  // 相同条件点击查询也要刷新。
  refresh.value++
}
const refresh = ref(0)
function exportCSV() {
  const p = params.value
  error.value = p.error
  if (!p.error) location.href = '/api/history/export?' + p.qs
}
watch(() => state.current, () => { result.value = null; error.value = '' }, { flush: 'sync' })
</script>

<template>
  <div class="card q">
    <div class="row">
      <span class="k">时间</span>
      <input v-model="start" class="input t" type="datetime-local" step="1" />
      <span class="muted">至</span>
      <input v-model="end" class="input t" type="datetime-local" step="1" />
      <div class="quick"><span class="muted">最近</span><button v-for="q in quickList" :key="q.ms" class="btn sm" @click="quick(q.ms)">{{ q.label }}</button></div>
    </div>
    <div class="row">
      <span class="k">点位</span>
      <SelectedTags :model-value="selected" @update:model-value="setSelected">
        <button class="btn sm" @click="picking = !picking">{{ picking ? '收起' : '选择点位' }}（{{ selected.length }}/{{ maxTags }}）</button>
      </SelectedTags>
      <div class="acts">
        <button class="btn" :disabled="loading" @click="exportCSV">导出 CSV</button>
        <button class="btn primary" :disabled="loading" @click="query">{{ loading ? '查询中…' : '查询' }}</button>
      </div>
    </div>
    <div class="hint muted">最长 {{ fmtDuration(maxRange / 1000) }}、最多 {{ maxTags }} 个点位；时间为服务器时钟（与数据的 ts 一致）。拖动点位标签可调整表格和导出列顺序。</div>
    <div v-if="error" class="err" role="alert">{{ error }}</div>
  </div>
  <TagPicker v-if="picking" :model-value="selected" :options="allColumns" :max="maxTags" @update:model-value="setSelected" @close="picking = false" />
  <HistoryTable :query="result?.qs || ''" :revision="refresh" :columns="resultCols" :title="result?.title" @loading="loading = $event" />
</template>

<style scoped>
.q { padding: 12px 16px; margin-bottom: 12px }
.row { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; padding: 4px 0 }
.k { font-weight: 700; font-size: 13px; width: 32px }
.t { width: 200px; padding: 6px 10px }
.quick { display: flex; align-items: center; gap: 6px; flex-wrap: wrap; margin-left: 12px; font-size: 12.5px }
.btn.sm { padding: 4px 10px; font-size: 12.5px }
.btn:disabled { opacity: .6; cursor: wait }
.acts { display: flex; gap: 8px; margin-left: auto }
.hint { font-size: 12px; margin-top: 4px }
.err { margin-top: 8px; color: var(--danger); font-size: 13px; font-weight: 600 }
</style>
