<script setup>
// 通讯预警：
//   当前状态 —— 全部炉台的心跳和时钟状态，每 5 秒刷新，异常的排在最前
//   告警记录 —— web_alarm 里的触发 / 恢复配成一次次告警，可筛选、导出 CSV、跳到历史曲线
// 红 / 黄标记、排序、卡片数字都按已记录的告警（防抖后）：时间不一致连续 3 轮超限才变黄，和告警记录一致。
// “状态”“心跳未变化”“工控机时钟”几列显示的是实时值。
import { ref, computed } from 'vue'
import { useRouter } from 'vue-router'
import AlarmHistory from '../components/AlarmHistory.vue'
import {
  state, alarmFlags, massOutage, selectFurnace, serverNow,
  fmtDuration, fmtSkew, fmtTime, fmtAlarmValue, STATUS_TEXT, STATUS_CLASS, ALARM_TEXT,
} from '../store.js'

const router = useRouter()
const tab = ref('now')
const onlyAbnormal = ref(false)
const area = ref('')
const q = ref('')

const areas = computed(() => [...new Set(state.furnaces.map((f) => f.area).filter(Boolean))].sort())
// 排序：心跳停止告警 → 时间不一致告警 → 无数据 → 正常
const rank = (f) => {
  const a = alarmFlags(f.device_ip)
  return a.stop ? 0 : a.skew ? 1 : f.status === 'no_data' ? 2 : 3
}
// 告警持续时长：心跳停止为已停多久（后端每 5 秒更新），时间不一致为从触发到现在（服务器时钟）
const alarmText = (a) => (a.alarm_type === 'heartbeat_stop'
  ? fmtAlarmValue(a.alarm_type, a.seconds)
  : `已 ${fmtDuration((serverNow() - a.since) / 1000)}`)
const rows = computed(() => {
  const kw = q.value.trim().toLowerCase()
  return state.furnaces
    .filter((f) => !area.value || f.area === area.value)
    .filter((f) => !kw || f.device_id.toLowerCase().includes(kw) || f.device_ip.includes(kw))
    .filter((f) => !onlyAbnormal.value || rank(f) < 2)
    .slice()
    .sort((a, b) => rank(a) - rank(b) || a.device_id.localeCompare(b.device_id))
})
const counts = computed(() => ({
  total: state.furnaces.length,
  online: state.furnaces.filter((f) => f.status === 'online').length,
  heartbeat: state.alarms.filter((a) => a.alarm_type === 'heartbeat_stop').length,
  skew: state.alarms.filter((a) => a.alarm_type === 'time_skew').length,
}))

function open(f) {
  selectFurnace(f.table)
  router.push('/data')
}
</script>

<template>
  <div v-if="massOutage" class="outage">
    超过一半的炉台同时心跳停止，多半是采集程序（kx-opc-client）或数据库的问题，而不是炉台本身，请先检查采集服务。
  </div>

  <div class="stats">
    <div class="stat card"><div class="k">炉台总数</div><div class="v num">{{ counts.total }}</div></div>
    <div class="stat card ok"><div class="k">在线</div><div class="v num">{{ counts.online }}</div></div>
    <div class="stat card danger"><div class="k">心跳停止</div><div class="v num">{{ counts.heartbeat }}</div></div>
    <div class="stat card warn"><div class="k">时间不一致（超过 {{ fmtDuration(state.settings.skew_limit_s || 1800) }}）</div><div class="v num">{{ counts.skew }}</div></div>
  </div>

  <div class="bar">
    <div class="tabs">
      <button class="tab" :class="{ on: tab === 'now' }" @click="tab = 'now'">当前状态</button>
      <button class="tab" :class="{ on: tab === 'history' }" @click="tab = 'history'">告警记录</button>
    </div>
  </div>

  <div v-if="tab === 'now'" class="card">
    <div class="hd">
      <span>炉台通讯状态</span>
      <span class="muted upd">更新于 {{ fmtTime(state.checkedAt) }}（基准时间）</span>
      <div class="tools">
        <input v-model="q" class="input" placeholder="炉台号 / IP" />
        <select v-model="area" class="input">
          <option value="">全部区域</option>
          <option v-for="a in areas" :key="a" :value="a">{{ a }} 区</option>
        </select>
        <label class="only"><input v-model="onlyAbnormal" type="checkbox" /> 只看异常</label>
      </div>
    </div>
    <div class="wrap">
      <table class="grid">
        <thead>
          <tr><th>炉台号</th><th>IP</th><th>区域</th><th>状态</th><th>心跳未变化</th><th>心跳时间（UTC）</th><th>工控机时钟</th><th>当前告警</th><th></th></tr>
        </thead>
        <tbody>
          <tr v-for="f in rows" :key="f.table" :class="{ stop: alarmFlags(f.device_ip).stop, skew: !alarmFlags(f.device_ip).stop && alarmFlags(f.device_ip).skew }">
            <td><b>{{ f.device_id }}</b></td>
            <td class="num muted">{{ f.device_ip }}</td>
            <td>{{ f.area }}</td>
            <td><span class="chip" :class="STATUS_CLASS[f.status]">{{ STATUS_TEXT[f.status] }}</span></td>
            <td class="num" :class="{ bad: f.status === 'stopped' }">{{ f.status === 'no_data' ? '—' : fmtDuration(f.heartbeat_age_s) }}</td>
            <td class="num muted">{{ f.heartbeat || '—' }}</td>
            <td><span :class="{ chip: alarmFlags(f.device_ip).skew, warn: alarmFlags(f.device_ip).skew }">{{ fmtSkew(f.skew_s) }}</span></td>
            <td>
              <span v-for="a in alarmFlags(f.device_ip).list" :key="a.alarm_type" class="chip" :class="a.alarm_type === 'heartbeat_stop' ? 'danger' : 'warn'"
                :title="`开始于 ${fmtTime(a.since)}（服务器时钟）`">{{ ALARM_TEXT[a.alarm_type] }} · {{ alarmText(a) }}</span>
            </td>
            <td><button class="btn" @click="open(f)">查看</button></td>
          </tr>
          <tr v-if="!rows.length"><td colspan="9" class="empty">没有符合条件的炉台</td></tr>
        </tbody>
      </table>
    </div>
  </div>

  <AlarmHistory v-else />
</template>

<style scoped>
.outage { margin-bottom: 12px; padding: 10px 14px; border-radius: 12px; background: var(--danger-soft); color: var(--danger);
  border: 1px solid #f6caca; font-weight: 600 }
.stats { display: grid; grid-template-columns: repeat(auto-fit, minmax(180px, 1fr)); gap: 12px; margin-bottom: 14px }
.stat { padding: 12px 16px }
.stat .k { font-size: 12px; color: var(--muted); font-weight: 600 }
.stat .v { font-size: 26px; font-weight: 800 }
.stat.ok .v { color: var(--ok) }
.stat.danger .v { color: var(--danger) }
.stat.warn .v { color: var(--warn) }
.bar { display: flex; align-items: center; gap: 12px; margin-bottom: 12px }
.tabs { display: flex; gap: 4px; background: #fff; border: 1px solid var(--line); padding: 3px; border-radius: 11px }
.tab { border: 0; background: none; padding: 6px 14px; border-radius: 8px; font-weight: 600; color: var(--muted); cursor: pointer }
.tab.on { background: var(--brand-soft); color: var(--brand) }
.hd { flex-wrap: wrap }
.upd { font-weight: 500; font-size: 12px }
.tools { margin-left: auto; display: flex; gap: 8px; align-items: center }
.tools .input { width: 150px; padding: 6px 10px }
.only { display: flex; align-items: center; gap: 5px; font-weight: 600; font-size: 12.5px; white-space: nowrap }
.wrap { overflow: auto; max-height: calc(100vh - 300px) }
.bad { color: var(--danger); font-weight: 700 }
/* 告警颜色：心跳停止红色，时间不一致黄色（整行淡底色 + 左侧色条） */
.stat.danger { box-shadow: inset 4px 0 0 var(--danger), var(--shadow) }
.stat.warn { box-shadow: inset 4px 0 0 #eab308, var(--shadow) }
tr.stop td { background: var(--danger-soft) !important }
tr.stop td:first-child { box-shadow: inset 3px 0 0 var(--danger) }
tr.skew td { background: var(--warn-soft) !important }
tr.skew td:first-child { box-shadow: inset 3px 0 0 #eab308 }
td .chip + .chip { margin-left: 4px }
</style>
