<script setup>
// 告警记录：按时间、炉台、类型查询，每行一次告警（触发到恢复）；可导出 CSV，可跳到历史曲线看当时的数据。
// 时间都是服务器时钟，与采集数据一致。
import { ref, computed } from 'vue'
import { useRouter } from 'vue-router'
import { state, getJSON, selectFurnace, toInput, fromInput, fmtTime, fmtDuration, fmtAlarmValue, ALARM_TEXT, serverNow } from '../store.js'

const router = useRouter()
const QUICK = [
  { label: '24 小时', ms: 86400000 },
  { label: '7 天', ms: 7 * 86400000 },
  { label: '30 天', ms: 30 * 86400000 },
]
const end = ref(toInput(serverNow()))
const start = ref(toInput(serverNow() - 86400000))
const device = ref('')
const type = ref('')

const loading = ref(false)
const error = ref('')
const result = ref(null) // { episodes, truncated }

const params = computed(() => {
  const from = fromInput(start.value)
  const to = fromInput(end.value)
  let err = ''
  if (!(from < to)) err = '开始时间必须早于结束时间'
  else if (to - from > state.settings.alarm_max_range_ms) err = `时间范围不能超过 ${fmtDuration(state.settings.alarm_max_range_ms / 1000)}`
  const qs = new URLSearchParams({ from, to })
  if (device.value.trim()) qs.set('device', device.value.trim())
  if (type.value) qs.set('type', type.value)
  return { err, qs: qs.toString() }
})

async function query() {
  error.value = params.value.err
  if (error.value) return
  loading.value = true
  try {
    result.value = await getJSON('/api/alarms?' + params.value.qs)
  } catch (e) {
    error.value = '查询失败：' + e.message
  } finally {
    loading.value = false
  }
}
function quick(ms) {
  const e = serverNow()
  end.value = toInput(e)
  start.value = toInput(e - ms)
  query()
}
function exportCSV() {
  error.value = params.value.err
  if (!error.value) location.href = '/api/alarms?format=csv&' + params.value.qs
}

// 跳到历史曲线：告警前后各留 5 分钟，不超过历史曲线的最长范围
function showCurve(ep) {
  const f = state.furnaces.find((x) => x.device_ip === ep.device_ip)
  if (!f) return
  const max = state.settings.history_max_range_ms
  const from = (ep.start ?? ep.end) - 5 * 60000
  const end = ep.alarm_type === 'heartbeat_jump' ? ep.start : ep.end
  const to = Math.min(end ? end + 5 * 60000 : serverNow(), from + max)
  selectFurnace(f.table)
  router.push({ path: '/curves/history/time', query: { from, to } })
}

const summary = computed(() => {
  const eps = result.value?.episodes || []
  return { total: eps.length, open: eps.filter((e) => !e.end && e.alarm_type !== 'heartbeat_jump').length, furnaces: new Set(eps.map((e) => e.device_ip)).size }
})

query()
</script>

<template>
  <div class="card q">
    <div class="row">
      <input v-model="start" class="input t" type="datetime-local" step="1" />
      <span class="muted">至</span>
      <input v-model="end" class="input t" type="datetime-local" step="1" />
      <span class="muted">最近</span>
      <button v-for="x in QUICK" :key="x.ms" class="btn sm" @click="quick(x.ms)">{{ x.label }}</button>
    </div>
    <div class="row">
      <input v-model="device" class="input d" placeholder="炉台号或 IP（空为全部）" @keydown.enter="query" />
      <select v-model="type" class="input d">
        <option value="">全部类型</option>
        <option value="heartbeat_stop">心跳停止</option>
        <option value="time_skew">时间不一致</option>
        <option value="heartbeat_jump">心跳跳变</option>
      </select>
      <div class="acts">
        <button class="btn" @click="exportCSV">导出 CSV</button>
        <button class="btn primary" :disabled="loading" @click="query">{{ loading ? '查询中…' : '查询' }}</button>
      </div>
    </div>
    <div v-if="error" class="err">{{ error }}</div>
  </div>

  <div class="card">
    <div class="hd">
      <span v-if="result">共 {{ summary.total }} 次告警，涉及 {{ summary.furnaces }} 台炉台<template v-if="summary.open">，{{ summary.open }} 次未恢复</template></span>
      <span v-if="result?.truncated" class="chip warn">记录太多，只显示了一部分，请缩小时间范围</span>
      <span class="muted hint">时间为服务器时钟</span>
    </div>
    <div class="wrap">
      <table class="grid">
        <thead>
          <tr><th>炉台号</th><th>IP</th><th>类型</th><th>开始</th><th>恢复</th><th>持续</th><th>触发时</th><th></th></tr>
        </thead>
        <tbody>
          <tr v-for="(ep, i) in result?.episodes || []" :key="i">
            <td><b>{{ ep.device_id }}</b></td>
            <td class="num muted">{{ ep.device_ip }}</td>
            <td><span class="chip" :class="ep.alarm_type === 'heartbeat_stop' ? 'danger' : 'warn'">{{ ALARM_TEXT[ep.alarm_type] }}</span></td>
            <td class="num">{{ ep.start ? fmtTime(ep.start) : '（范围之前）' }}</td>
            <td class="num"><span v-if="ep.end">{{ fmtTime(ep.end) }}</span><span v-else-if="ep.alarm_type === 'heartbeat_jump'">—</span><span v-else class="chip danger">未恢复</span></td>
            <td class="num">{{ ep.duration_s != null ? fmtDuration(ep.duration_s) : '—' }}</td>
            <td>{{ fmtAlarmValue(ep.alarm_type, ep.seconds) }}</td>
            <td><button class="btn sm" @click="showCurve(ep)">看曲线</button></td>
          </tr>
          <tr v-if="result && !result.episodes?.length"><td colspan="8" class="empty">这段时间没有告警</td></tr>
        </tbody>
      </table>
    </div>
  </div>
</template>

<style scoped>
.q { padding: 12px 16px; margin-bottom: 12px }
.row { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; padding: 4px 0; font-size: 12.5px }
.t { width: 200px; padding: 6px 10px }
.d { width: 200px; padding: 6px 10px }
.btn.sm { padding: 4px 10px; font-size: 12.5px }
.acts { display: flex; gap: 8px; margin-left: auto }
.err { margin-top: 8px; color: var(--danger); font-size: 13px; font-weight: 600 }
.hint { margin-left: auto; font-weight: 500; font-size: 12px }
.wrap { overflow: auto; max-height: calc(100vh - 380px) }
</style>
