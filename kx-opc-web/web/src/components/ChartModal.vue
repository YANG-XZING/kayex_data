<script setup>
// 曲线放大窗口：盖在页面上，点空白处或按 Esc 关闭。标题栏内容由使用方通过插槽给出。
import { onMounted, onBeforeUnmount } from 'vue'

const emit = defineEmits(['close'])
function onKey(e) { if (e.key === 'Escape') emit('close') }
onMounted(() => window.addEventListener('keydown', onKey))
onBeforeUnmount(() => window.removeEventListener('keydown', onKey))
</script>

<template>
  <Teleport to="body">
    <div class="modal" @click.self="emit('close')">
      <div class="panel card">
        <div class="ph">
          <slot name="head" />
          <span class="muted tip">按住鼠标拖动放大，双击还原，点图例隐藏曲线，Esc 关闭</span>
          <slot name="actions" />
          <button class="btn sm" @click="emit('close')">关闭</button>
        </div>
        <slot />
      </div>
    </div>
  </Teleport>
</template>

<style scoped>
.modal { position: fixed; inset: 0; z-index: 50; background: rgba(15, 23, 42, .45); display: grid; place-items: center; padding: 24px }
.panel { width: min(1600px, 100%); padding: 12px 16px 14px }
.ph { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; margin-bottom: 8px; font-size: 13px }
.ph .tip { margin-left: auto; font-size: 12px }
.btn.sm { padding: 4px 10px; font-size: 12.5px }
</style>
