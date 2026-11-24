<script setup>
// 历史查询和全量导出预览共用，只读取当前页；查询条件由父页面提交后固定。
import { ref, shallowRef, computed, watch, onBeforeUnmount } from 'vue'
import Pager from './Pager.vue'
import PageSizePicker from './PageSizePicker.vue'
import { getJSON, labelOf, fmtField, fmtTime } from '../store.js'

const props = defineProps({
  query: { type: String, default: '' },
  columns: { type: Array, default: () => [] },
  endpoint: { type: String, default: '/api/history/page' },
  title: String,
  revision: Number,
})
const emit = defineEmits(['loading'])
const pageSize = ref(10)
const page = ref(1)
const total = ref(0)
const data = shallowRef(null)
const loading = ref(false)
const error = ref('')
const ms = ref(0)
let controller = null
let generation = 0
const tableCols = computed(() => props.columns.map((key) => ({ key, label: labelOf(key) })))

function cancel() { generation++; controller?.abort(); controller = null }
async function fetchPage(target = 1) {
  cancel()
  if (!props.query) { loading.value = false; emit('loading', false); return }
  const current = generation
  controller = new AbortController()
  loading.value = true
  emit('loading', true)
  error.value = ''
  const started = performance.now()
  try {
    const qs = new URLSearchParams(props.query)
    qs.set('offset', (target - 1) * pageSize.value)
    qs.set('limit', pageSize.value)
    const result = await getJSON(props.endpoint + '?' + qs, { signal: controller.signal })
    if (current !== generation) return
    if (typeof result.total === 'number') total.value = result.total
    data.value = result
    page.value = target
    ms.value = Math.round(performance.now() - started)
  } catch (e) {
    if (current === generation) error.value = '查询失败：' + e.message
  } finally {
    if (current === generation) { loading.value = false; emit('loading', false) }
  }
}
watch([() => props.query, () => props.endpoint, () => props.revision, pageSize], () => {
  data.value = null
  total.value = 0
  page.value = 1
  fetchPage(1)
}, { immediate: true })
onBeforeUnmount(cancel)
function time(ms) { return fmtTime(ms) + '.' + String(Math.round(ms) % 1000).padStart(3, '0') }
</script>

<template>
  <div v-if="query" class="card list" :aria-busy="loading">
    <div class="list-head">
      <div class="heading"><b>{{ title || '查询结果' }}</b><span v-if="data" class="muted timing">本页耗时 {{ ms }} 毫秒</span></div>
      <PageSizePicker v-model="pageSize" :disabled="loading" />
    </div>
    <div v-if="error" class="error" role="alert">{{ error }} <button class="btn sm" :disabled="loading" @click="fetchPage(page)">重试</button></div>
    <div v-if="!data" class="empty"><div class="big">{{ loading ? '查询中…' : '未能读取数据，请重试' }}</div></div>
    <div v-else-if="!data.ts.length" class="empty"><div class="big">这段时间没有数据</div><div>可以换个时间段或炉台查询</div></div>
    <div v-else class="wrap" :class="{ busy: loading }">
      <table class="grid">
        <thead><tr><th class="idx">#</th><th>时间</th><th v-for="c in tableCols" :key="c.key" :title="c.key">{{ c.label }}<span v-if="c.label !== c.key" class="sub">{{ c.key }}</span></th></tr></thead>
        <tbody>
          <tr v-for="(ts, i) in data.ts" :key="ts">
            <td class="idx num">{{ (page - 1) * pageSize + i + 1 }}</td>
            <td class="num">{{ time(ts) }}</td>
            <td v-for="c in tableCols" :key="c.key" class="num">{{ fmtField(c.key, data.values[c.key]?.[i]) }}</td>
          </tr>
        </tbody>
      </table>
    </div>
    <div v-if="data" class="footer">
      <span class="muted range">{{ loading ? '查询中…' : (data.ts.length ? `当前第 ${(page - 1) * pageSize + 1}–${(page - 1) * pageSize + data.ts.length} 条` : '当前无数据') }}</span>
      <Pager :model-value="page" :total="total" :page-size="pageSize" :disabled="loading" @update:model-value="fetchPage" />
    </div>
  </div>
  <div v-else class="card empty"><div class="big">选择时间和点位后点“查询”</div><div>查询后可翻页查看，也可导出完整时间段的 CSV</div></div>
</template>

<style scoped>
.list { padding: 12px 14px }
.list-head { display: flex; align-items: center; justify-content: space-between; gap: 12px; flex-wrap: wrap; margin-bottom: 12px; font-size: 13px }
.heading { display: flex; align-items: center; gap: 12px; flex-wrap: wrap }
.timing { font-size: 12px }
.wrap { overflow: auto; max-height: 620px; border: 1px solid var(--line2); border-radius: 10px }
.wrap.busy { opacity: .5 }
.grid td { font-size: 12.5px }
.grid th { font-size: 12px }
.grid tbody tr:nth-child(even) { background: #fafbfe }
.idx { width: 54px; color: var(--faint) }
.sub { margin-left: 5px; font-weight: 400; color: var(--faint); font-size: 11px }
.footer { display: flex; gap: 12px; flex-wrap: wrap; align-items: center; justify-content: space-between; padding-top: 12px }
.footer :deep(.pager) { flex: 1 }
.range { font-size: 12px; white-space: nowrap }
.error { padding: 8px 0; color: var(--danger); font-size: 13px }
.btn.sm { padding: 4px 10px; font-size: 12px; margin-left: 6px }
@media (max-width: 700px) { .footer :deep(.pager) { flex-basis: 100% } }
</style>
