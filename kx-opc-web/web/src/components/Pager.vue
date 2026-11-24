<script setup>
// 翻页条：首页 / 上一页 / 第几页（可输入跳转）/ 下一页 / 尾页，以及总条数。v-model 为当前页（从 1 开始）
import { ref, computed, watch } from 'vue'

const props = defineProps({
  modelValue: Number,
  total: Number,    // 总条数
  pageSize: Number,
  disabled: Boolean, // 读取中
})
const emit = defineEmits(['update:modelValue'])

const pages = computed(() => Math.max(1, Math.ceil(props.total / props.pageSize)))
const input = ref(props.modelValue)
watch(() => props.modelValue, (p) => { input.value = p })

function go(p) {
  p = Math.min(Math.max(1, Math.round(Number(p)) || 1), pages.value)
  input.value = p
  if (p !== props.modelValue) emit('update:modelValue', p)
}
</script>

<template>
  <div class="pager">
    <span class="muted">共 <b class="num">{{ total.toLocaleString() }}</b> 条，{{ pages }} 页</span>
    <button class="btn sm" :disabled="disabled || modelValue <= 1" @click="go(1)">首页</button>
    <button class="btn sm" :disabled="disabled || modelValue <= 1" @click="go(modelValue - 1)">上一页</button>
    <input v-model="input" class="input pageno num" type="number" min="1" :max="pages" :disabled="disabled"
      title="输入页码后回车跳转" @keydown.enter="go(input)" @blur="go(input)" />
    <span class="muted">/ {{ pages }}</span>
    <button class="btn sm" :disabled="disabled || modelValue >= pages" @click="go(modelValue + 1)">下一页</button>
    <button class="btn sm" :disabled="disabled || modelValue >= pages" @click="go(pages)">尾页</button>
  </div>
</template>

<style scoped>
.pager { display: flex; align-items: center; justify-content: flex-end; gap: 8px; flex-wrap: wrap; font-size: 12.5px }
.pager .muted:first-child { margin-right: auto }
.btn.sm { padding: 5px 12px; font-size: 12.5px }
.btn:disabled { opacity: .45; cursor: default }
.pageno { width: 70px; padding: 5px 8px; text-align: center }
</style>
