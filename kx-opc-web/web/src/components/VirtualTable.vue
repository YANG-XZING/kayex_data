<script setup>
// 大表格：只渲染看得见的几十行（固定行高），几千上万行滚动也不卡
import { ref, computed, onMounted, onBeforeUnmount } from 'vue'

const props = defineProps({
  columns: Array, // [{ key, label, sub }]
  count: Number, // 行数
  cell: Function, // (row, key) => 文本
  height: { type: Number, default: 560 },
})

const ROW = 30
const OVERSCAN = 10
const box = ref(null)
const top = ref(0)
const onScroll = () => { top.value = box.value.scrollTop }

const range = computed(() => {
  const start = Math.max(0, Math.floor(top.value / ROW) - OVERSCAN)
  const end = Math.min(props.count, Math.ceil((top.value + props.height) / ROW) + OVERSCAN)
  return { start, end }
})
const rows = computed(() => {
  const out = []
  for (let r = range.value.start; r < range.value.end; r++) out.push(r)
  return out
})

let ro = null
onMounted(() => {
  ro = new ResizeObserver(onScroll)
  ro.observe(box.value)
})
onBeforeUnmount(() => ro?.disconnect())
defineExpose({ scrollTop: () => { if (box.value) box.value.scrollTop = 0 } })
</script>

<template>
  <div ref="box" class="vt" :style="{ height: height + 'px' }" @scroll.passive="onScroll">
    <table class="grid">
      <thead>
        <tr>
          <th class="idx">#</th>
          <th v-for="c in columns" :key="c.key" :title="c.key">{{ c.label }}<span v-if="c.sub" class="sub">{{ c.sub }}</span></th>
        </tr>
      </thead>
      <tbody>
        <tr :style="{ height: range.start * ROW + 'px' }" class="pad"><td :colspan="columns.length + 1"></td></tr>
        <tr v-for="r in rows" :key="r">
          <td class="idx num">{{ r + 1 }}</td>
          <td v-for="c in columns" :key="c.key" class="num">{{ cell(r, c.key) }}</td>
        </tr>
        <tr :style="{ height: (count - range.end) * ROW + 'px' }" class="pad"><td :colspan="columns.length + 1"></td></tr>
      </tbody>
    </table>
  </div>
</template>

<style scoped>
.vt { overflow: auto; border: 1px solid var(--line); border-radius: 12px; background: #fff }
table.grid td { height: 30px; padding: 0 12px; font-size: 12.5px }
table.grid th { font-size: 12px }
table.grid tr.pad td { padding: 0; border: 0; height: 0 }
.idx { color: var(--muted); width: 1%; position: sticky; left: 0; background: #fff }
th.idx { background: #f7f8fc; z-index: 2 }
.sub { margin-left: 4px; font-weight: 500; color: var(--faint); font-size: 11px }
tbody tr:nth-child(even) td { background: #fafbfe }
</style>
