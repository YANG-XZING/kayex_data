<script setup>
// 查看数据：实时数据（每秒刷新的表格）和历史数据（按时间查询的表格，可导出 CSV），用标签切换（?tab=history）。
// 客户账户的“上位机数据”（/hmi/data）也是这个页面：后端只给客户可见的点位，页面不用区分账户。
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import FurnaceHeader from '../components/FurnaceHeader.vue'
import Realtime from './Realtime.vue'
import History from './History.vue'
import { currentFurnace } from '../store.js'

const TABS = [
  { key: 'realtime', label: '实时数据' },
  { key: 'history', label: '历史数据' },
]
const route = useRoute()
const router = useRouter()
// 旧链接 ?tab=chart：曲线已经单独成一个菜单
if (route.query.tab === 'chart') router.replace({ path: '/curves/history', query: { ...route.query, tab: undefined } })
const tab = computed(() => (route.query.tab === 'history' ? 'history' : 'realtime'))
function go(t) {
  if (t !== tab.value) router.replace({ path: route.path, query: t === 'realtime' ? {} : { tab: t } })
}
</script>

<template>
  <div v-if="!currentFurnace" class="card empty">
    <div class="big">请先选择炉台</div>
    <div>在上方输入炉台号或 IP</div>
  </div>
  <template v-else>
    <FurnaceHeader>
      <div class="dtabs">
        <button v-for="t in TABS" :key="t.key" class="dtab" :class="{ on: tab === t.key }" @click="go(t.key)">{{ t.label }}</button>
      </div>
    </FurnaceHeader>
    <Realtime v-if="tab === 'realtime'" />
    <History v-else />
  </template>
</template>

<style scoped>
.dtabs { display: inline-flex; gap: 4px; background: #f3f5fb; padding: 3px; border-radius: 11px; margin-left: auto }
.dtab { border: 0; background: none; padding: 6px 16px; border-radius: 8px; font-weight: 700; color: var(--muted); cursor: pointer }
.dtab.on { background: #fff; color: var(--brand); box-shadow: 0 1px 3px rgba(15, 23, 42, .1) }
</style>
