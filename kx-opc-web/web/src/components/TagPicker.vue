<script setup>
// 点位多选：按点位名或中文名搜索，最多选 max 个；v-model 为点位名数组（保持选择顺序）
import { ref, computed } from 'vue'
import { labelOf } from '../store.js'

const props = defineProps({
  modelValue: Array,
  options: Array, // 可选的点位名
  max: Number,
  allowSelectAll: Boolean,
})
const emit = defineEmits(['update:modelValue', 'close'])

const filter = ref('')
const candidates = computed(() => {
  const q = filter.value.trim().toLowerCase()
  return props.options.filter((c) => !q || c.toLowerCase().includes(q) || labelOf(c).toLowerCase().includes(q))
})
function selectAll() {
  const kept = props.modelValue.filter((c) => props.options.includes(c))
  const added = props.options.filter((c) => !kept.includes(c))
  emit('update:modelValue', [...kept, ...added].slice(0, props.max))
}
function toggle(col) {
  const s = props.modelValue
  if (s.includes(col)) emit('update:modelValue', s.filter((c) => c !== col))
  else if (s.length < props.max) emit('update:modelValue', [...s, col])
}
</script>

<template>
  <div class="card pick">
    <div class="pick-hd">
      <input v-model="filter" class="input" placeholder="点位名或中文名" />
      <span class="muted">已选 {{ modelValue.length }} / {{ max }}</span>
      <button v-if="allowSelectAll" class="btn" @click="selectAll">全选</button>
      <button class="btn" @click="emit('update:modelValue', [])">清空</button>
      <button class="btn primary" @click="emit('close')">完成</button>
    </div>
    <div class="cands">
      <label v-for="c in candidates" :key="c" class="cand" :class="{ on: modelValue.includes(c) }" :title="c">
        <input type="checkbox" :checked="modelValue.includes(c)" :disabled="!modelValue.includes(c) && modelValue.length >= max"
          @change="toggle(c)" />
        <span class="nm">{{ labelOf(c) }}</span>
        <span v-if="labelOf(c) !== c" class="pt">{{ c }}</span>
      </label>
      <div v-if="!candidates.length" class="muted none">没有匹配的点位</div>
    </div>
  </div>
</template>

<style scoped>
.pick { padding: 12px; margin-bottom: 12px }
.pick-hd { display: flex; gap: 8px; align-items: center; margin-bottom: 10px }
.pick-hd .input { max-width: 260px }
.pick-hd .muted { font-size: 12.5px; margin-right: auto }
.cands { display: grid; grid-template-columns: repeat(auto-fill, minmax(220px, 1fr)); gap: 4px; max-height: 300px; overflow-y: auto }
.cand { display: flex; align-items: center; gap: 7px; padding: 5px 8px; border-radius: 8px; font-size: 12.5px; cursor: pointer; min-width: 0 }
.cand:hover { background: #f3f5fb }
.cand.on { background: var(--brand-soft); color: var(--brand-ink); font-weight: 600 }
.nm, .pt { overflow: hidden; text-overflow: ellipsis; white-space: nowrap }
.pt { color: var(--faint); font-size: 11px; font-weight: 400 }
.none { padding: 12px }
</style>
