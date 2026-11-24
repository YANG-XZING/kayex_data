<script setup>
// 当前炉台的状态条：在线状态、数据时间与新鲜度、工控机时钟偏差
import { computed } from 'vue'
import { state, currentFurnace, alarmFlags, dotClass, fmtTime, fmtDuration, fmtSkew, STATUS_TEXT, STATUS_CLASS } from '../store.js'

const skewAlarm = computed(() => currentFurnace.value && alarmFlags(currentFurnace.value.device_ip).skew)
const age = computed(() => {
  if (!state.live.ts || !state.serverNow) return null
  return Math.max(0, (state.serverNow - state.live.ts) / 1000)
})
</script>

<template>
  <div v-if="currentFurnace" class="fh card">
    <div class="id">
      <span class="dot" :class="dotClass(currentFurnace)"></span>
      <b>{{ currentFurnace.device_id }}</b>
      <span class="muted num">{{ currentFurnace.device_ip }}</span>
      <span v-if="currentFurnace.area" class="chip brand">{{ currentFurnace.area }} 区</span>
    </div>
    <div class="kv">
      <span class="k">状态</span>
      <span class="chip" :class="STATUS_CLASS[currentFurnace.status]">{{ STATUS_TEXT[currentFurnace.status] }}</span>
    </div>
    <div class="kv">
      <span class="k">数据时间</span>
      <span class="num">{{ fmtTime(state.live.ts) }}</span>
      <span v-if="age !== null" class="muted" :class="{ stale: age > 10 }">（{{ age < 3 ? '刚刚' : fmtDuration(age) + '前' }}）</span>
    </div>
    <div class="kv">
      <span class="k">工控机时钟</span>
      <span :class="{ warnText: skewAlarm }">{{ fmtSkew(currentFurnace.skew_s) }}</span>
    </div>
    <slot />
  </div>
</template>

<style scoped>
.fh { display: flex; align-items: center; gap: 22px; padding: 11px 16px; margin-bottom: 14px; flex-wrap: wrap }
.id { display: flex; align-items: center; gap: 9px; font-size: 16px }
.kv { display: flex; align-items: center; gap: 7px; font-size: 13px }
.k { color: var(--muted); font-size: 12px; font-weight: 600 }
.stale { color: var(--danger) }
.warnText { color: var(--warn); font-weight: 700 }
</style>
