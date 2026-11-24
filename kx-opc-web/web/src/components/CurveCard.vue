<script setup>
// 曲线卡片：实时曲线、历史曲线共用。
//   页面上直接显示完整的图（铺满页面下方，可框选放大、双击还原、点图例隐藏曲线），鼠标是放大镜；
//   单击图或点“放大”弹出大图，大图从页面上当前的缩放范围开始，另行缩放。
//   标题栏内容由 #head 插槽给出（页面和弹窗共用）；x-range 为页面上的缩放范围（v-model），null 为全部 / 跟随最新。
import { ref } from 'vue'
import MultiChart from './MultiChart.vue'
import ChartModal from './ChartModal.vue'
import Icon from './Icon.vue'

const props = defineProps({
  series: Array,
  getData: Function,
  tick: Number,
  bands: Array,
  windowSec: Number,
  xRange: Array,
  tip: String,                                  // 标题栏右侧的操作提示
  resetLabel: { type: String, default: '还原缩放' }, // 缩放后恢复的按钮文字（实时曲线为“回到实时”）
})
const emit = defineEmits(['update:xRange'])

const MIN_HEIGHT = 360
const BIG_RATIO = 0.75 // 大图高度占窗口高度的比例
const chart = ref(null)
const big = ref(false)
const bigRange = ref(null)
const bigHeight = () => Math.max(320, Math.round(window.innerHeight * BIG_RATIO))
function openBig() {
  bigRange.value = props.xRange
  big.value = true
}

defineExpose({ resize: () => chart.value?.resize() }) // 上方内容高度变了时由父组件调用
</script>

<template>
  <div class="card plotcard">
    <div class="th">
      <slot name="head" />
      <span class="muted tip">{{ tip }}</span>
      <button v-if="xRange" class="btn sm" @click="emit('update:xRange', null)">{{ resetLabel }}</button>
      <button class="btn sm zoom" title="弹出大图" @click="openBig"><Icon name="zoomin" :size="14" />放大</button>
    </div>
    <MultiChart ref="chart" class="zoomable" :series="series" :get-data="getData" :tick="tick" :height="MIN_HEIGHT" fill interactive
      :bands="bands" :window-sec="windowSec" :x-range="xRange" @zoom="emit('update:xRange', $event)" @plotclick="openBig" />
  </div>

  <ChartModal v-if="big" @close="big = false">
    <template #head><slot name="head" /></template>
    <template #actions><button v-if="bigRange" class="btn sm" @click="bigRange = null">{{ resetLabel }}</button></template>
    <MultiChart :series="series" :get-data="getData" :tick="tick" :height="bigHeight()" interactive
      :bands="bands" :window-sec="windowSec" :x-range="bigRange" @zoom="bigRange = $event" />
  </ChartModal>
</template>

<style scoped>
.plotcard { padding: 10px 14px }
.th { display: flex; align-items: center; gap: 10px; font-size: 13px; margin-bottom: 8px }
.th .tip { margin-left: auto; font-size: 12px }
.btn.sm { padding: 4px 10px; font-size: 12.5px }
.zoom { display: inline-flex; align-items: center; gap: 4px }
/* 页面上的图：鼠标显示为放大镜，提示单击可放大 */
.zoomable :deep(.u-over) { cursor: zoom-in }
</style>
