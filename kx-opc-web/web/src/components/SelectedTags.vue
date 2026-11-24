<script setup>
// 已选点位：拖放排序，Alt + 左右方向键也可移动；顺序由调用方保存。
import { ref } from 'vue'
import { labelOf } from '../store.js'

const props = defineProps({ modelValue: { type: Array, default: () => [] } })
const emit = defineEmits(['update:modelValue'])
const dragging = ref('')
const target = ref('')
const after = ref(false)
const announcement = ref('')
function reset() { dragging.value = ''; target.value = '' }
function start(event, col) {
  dragging.value = col
  event.dataTransfer.effectAllowed = 'move'
  event.dataTransfer.setData('text/plain', col)
}
function over(event, col) {
  if (!dragging.value || dragging.value === col) return
  event.preventDefault()
  event.dataTransfer.dropEffect = 'move'
  target.value = col
  const rect = event.currentTarget.getBoundingClientRect()
  after.value = event.clientX >= rect.left + rect.width / 2
}
function drop(event, col) {
  if (dragging.value && dragging.value !== col) {
    const list = props.modelValue.filter((c) => c !== dragging.value)
    const index = list.indexOf(col)
    if (index >= 0) {
      list.splice(index + (after.value ? 1 : 0), 0, dragging.value)
      emit('update:modelValue', list)
      announcement.value = `${labelOf(dragging.value)}已移到第 ${list.indexOf(dragging.value) + 1} 位`
    }
  }
  reset()
}
function move(col, direction) {
  const list = [...props.modelValue]
  const index = list.indexOf(col)
  const next = index + direction
  if (index < 0 || next < 0 || next >= list.length) return
  ;[list[index], list[next]] = [list[next], list[index]]
  emit('update:modelValue', list)
  announcement.value = `${labelOf(col)}已移到第 ${next + 1} 位`
}
</script>

<template>
  <div class="selected-tags" aria-label="已选点位，可拖动排序">
    <span v-for="c in modelValue" :key="c" class="chip brand tag" draggable="true" tabindex="0"
      :class="{ dragging: dragging === c, before: target === c && !after, after: target === c && after }"
      :title="`${c}；拖动排序，或按 Alt + 左右方向键`"
      @dragstart="start($event, c)" @dragover="over($event, c)" @drop.prevent.stop="drop($event, c)" @dragend="reset"
      @dragleave="target === c && !($event.relatedTarget && $event.currentTarget.contains($event.relatedTarget)) && (target = '')"
      @keydown.alt.left.prevent="move(c, -1)" @keydown.alt.right.prevent="move(c, 1)">
      <span class="handle" aria-hidden="true">⠿</span>{{ labelOf(c) }}
      <button type="button" draggable="false" :aria-label="`移除${labelOf(c)}`" @mousedown.stop
        @click.stop="emit('update:modelValue', modelValue.filter((x) => x !== c))">×</button>
    </span>
    <slot />
    <span class="sr-only" role="status" aria-live="polite">{{ announcement }}</span>
  </div>
</template>

<style scoped>
.selected-tags { display: flex; gap: 7px; flex-wrap: wrap; align-items: center; flex: 1; min-width: 0 }
.tag { position: relative; padding: 4px 5px 4px 8px; cursor: grab; user-select: none; border: 1px solid transparent; transition: background .15s, opacity .15s }
.tag:hover, .tag:focus-visible { border-color: var(--brand2); outline: none }
.tag:active { cursor: grabbing }
.tag.dragging { opacity: .4 }
.tag.before::before, .tag.after::after { content: ''; position: absolute; top: 1px; bottom: 1px; width: 3px; border-radius: 2px; background: var(--brand) }
.tag.before::before { left: -5px }
.tag.after::after { right: -5px }
.handle { color: var(--brand2); font-size: 14px; line-height: 1 }
.tag button { border: 0; background: none; color: inherit; cursor: pointer; font-size: 15px; line-height: 1; padding: 2px 4px; border-radius: 50% }
.tag button:hover { background: #dce0ff }
.sr-only { position: absolute; width: 1px; height: 1px; padding: 0; overflow: hidden; clip-path: inset(50%); white-space: nowrap }
</style>
