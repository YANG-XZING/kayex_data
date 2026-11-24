<script setup>
// 每页条数：快捷选项或自定义正整数，确认后才提交，避免输入过程中重复查询。
import { ref, computed, watch } from 'vue'

const props = defineProps({ modelValue: { type: Number, default: 10 }, max: { type: Number, default: 1000 }, disabled: Boolean })
const emit = defineEmits(['update:modelValue'])
const presets = computed(() => [10, 20, 50, 100].filter((size) => size <= props.max))
const mode = ref('10')
const draft = ref('10')
const error = ref('')
function reset() {
  mode.value = presets.value.includes(props.modelValue) ? String(props.modelValue) : 'custom'
  draft.value = String(props.modelValue)
  error.value = ''
}
watch(() => props.modelValue, reset, { immediate: true })
function choose() {
  if (props.disabled) return
  error.value = ''
  if (mode.value === 'custom') draft.value = String(props.modelValue)
  else emit('update:modelValue', Number(mode.value))
}
function apply() {
  if (props.disabled) return
  const size = Number(draft.value)
  if (!String(draft.value).trim() || !Number.isInteger(size) || size < 1 || size > props.max) {
    error.value = `请输入 1–${props.max} 之间的整数`
    return
  }
  error.value = ''
  if (size !== props.modelValue) emit('update:modelValue', size)
}
</script>

<template>
  <div class="page-size">
    <label class="choice muted">每页
      <select v-model="mode" class="input" aria-label="每页条数" :disabled="disabled" @change="choose">
        <option v-for="size in presets" :key="size" :value="String(size)">{{ size }}</option>
        <option value="custom">自定义</option>
      </select>
    </label>
    <template v-if="mode === 'custom'">
      <input v-model="draft" class="input custom num" type="number" min="1" :max="max" step="1" :disabled="disabled"
        :aria-label="`自定义每页条数（1–${max}）`" :aria-invalid="!!error" :title="`输入 1–${max} 之间的整数，回车或点击确定`"
        @keydown.enter.prevent="apply" @keydown.esc.prevent="reset" @input="error = ''" />
      <button class="btn sm" :disabled="disabled" @click="apply">确定</button>
    </template>
    <span class="muted">条</span>
    <span v-if="error" class="error" role="alert">{{ error }}</span>
  </div>
</template>

<style scoped>
.page-size, .choice { display: flex; align-items: center; gap: 7px; flex-wrap: wrap; font-size: 12px }
.choice select { width: 92px; padding: 5px 8px }
.custom { width: 86px; padding: 5px 8px }
.btn.sm { padding: 5px 10px; font-size: 12px }
:disabled { opacity: .55; cursor: wait }
.error { color: var(--danger); font-size: 12px }
</style>
