<script setup>
// 曲线模块：当前炉台的状态条 + 实时曲线或历史曲线（由路由的 meta.curve 决定）
import { useRoute } from 'vue-router'
import FurnaceHeader from '../components/FurnaceHeader.vue'
import LiveCurves from './LiveCurves.vue'
import HistoryCurves from './HistoryCurves.vue'
import { currentFurnace } from '../store.js'

const route = useRoute()
</script>

<template>
  <div v-if="!currentFurnace" class="card empty">
    <div class="big">请先选择炉台</div>
    <div>在上方输入炉台号或 IP</div>
  </div>
  <template v-else>
    <FurnaceHeader />
    <LiveCurves v-if="route.meta.curve === 'live'" />
    <HistoryCurves v-else :key="route.fullPath" :mode="route.meta.hist" />
  </template>
</template>
