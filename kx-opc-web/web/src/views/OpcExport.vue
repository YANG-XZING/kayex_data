<script setup>
// OPC 全量导出：时间、点位选择与历史数据页一致，支持全部可见点位的分页预览和流式 CSV 导出。
import { ref, shallowRef, computed, watch } from 'vue'
import FurnaceHeader from '../components/FurnaceHeader.vue'
import TagPicker from '../components/TagPicker.vue'
import SelectedTags from '../components/SelectedTags.vue'
import HistoryTable from '../components/HistoryTable.vue'
import { state, currentFurnace, toInput, fromInput, fmtDuration, serverNow, quickRanges, load, save } from '../store.js'

const maxRange = computed(() => state.settings.export_max_range_ms || 31 * 86400000)
const quickList = computed(() => quickRanges(maxRange.value))
const end = ref(toInput(serverNow()))
const start = ref(toInput(serverNow() - 3600000))
function quick(ms) {
  const e = Math.floor(serverNow() / 1000) * 1000
  end.value = toInput(e)
  start.value = toInput(e - ms)
  query()
}

const allCols = computed(() => state.columns.map((c) => c.name))
const savedCols = load('kx.export.cols', [])
const selected = ref(Array.isArray(savedCols) ? savedCols : [])
const allSelected = ref(load('kx.export.colMode', 'all') !== 'custom')
function setSelected(list) {
  selected.value = list
  allSelected.value = allCols.value.length > 0 && list.length === allCols.value.length && allCols.value.every((c) => list.includes(c))
  save('kx.export.cols', list)
  save('kx.export.colMode', allSelected.value ? 'all' : 'custom')
}
watch(allCols, (list) => {
  if (!list.length) return
  const kept = selected.value.filter((c) => list.includes(c))
  setSelected(allSelected.value ? [...kept, ...list.filter((c) => !kept.includes(c))] : kept)
}, { immediate: true })
const picking = ref(false)
const cols = computed(() => selected.value.filter((c) => allCols.value.includes(c)))
const error = ref('')
const loading = ref(false)
const result = shallowRef(null)
const refresh = ref(0)
const params = computed(() => {
  const from = fromInput(start.value)
  const to = fromInput(end.value)
  let error = ''
  if (!currentFurnace.value) error = '请先选择炉台'
  else if (!cols.value.length) error = '请选择点位'
  else if (!(from < to)) error = '开始时间必须早于结束时间'
  else if (to - from > maxRange.value) error = `时间范围不能超过 ${fmtDuration(maxRange.value / 1000)}`
  return { error, qs: new URLSearchParams({ table: state.current, from, to, cols: cols.value.join(',') }).toString() }
})
const resultCols = computed(() => {
  const saved = result.value?.cols || []
  return cols.value.length === saved.length && cols.value.every((c) => saved.includes(c)) ? cols.value : saved
})
function query() {
  error.value = params.value.error
  if (error.value) return
  result.value = { qs: params.value.qs, cols: [...cols.value], title: `${currentFurnace.value.device_id} · ${start.value.replace('T', ' ')} 至 ${end.value.replace('T', ' ')}` }
  refresh.value++
}
function exportCSV() {
  error.value = params.value.error
  if (!error.value) location.href = '/api/export?' + params.value.qs
}
watch(() => state.current, () => { result.value = null; error.value = '' }, { flush: 'sync' })
</script>

<template>
  <div v-if="!currentFurnace" class="card empty">
    <div class="big">请先选择炉台</div>
    <div>在上方输入炉台号或 IP</div>
  </div>
  <template v-else>
    <FurnaceHeader />
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
        <SelectedTags :model-value="cols" @update:model-value="setSelected">
          <button class="btn sm" @click="picking = !picking">{{ picking ? '收起' : '选择点位' }}（{{ cols.length }}/{{ allCols.length }}）</button>
        </SelectedTags>
        <div class="acts">
          <button class="btn" :disabled="loading" @click="exportCSV">导出 CSV</button>
          <button class="btn primary" :disabled="loading" @click="query">{{ loading ? '查询中…' : '查询' }}</button>
        </div>
      </div>
      <div class="hint muted">最长 {{ fmtDuration(maxRange / 1000) }}；时间为服务器时钟（与数据的 ts 一致）。拖动点位标签可调整表格和导出列顺序，在“选择点位”中可全选。CSV 导出完整时间段的数据。</div>
      <div v-if="error" class="err" role="alert">{{ error }}</div>
    </div>
    <TagPicker v-if="picking" :model-value="cols" :options="allCols" :max="allCols.length" allow-select-all
      @update:model-value="setSelected" @close="picking = false" />
    <HistoryTable :query="result?.qs || ''" :revision="refresh" :columns="resultCols" :title="result?.title" endpoint="/api/export/page" @loading="loading = $event" />
  </template>
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
