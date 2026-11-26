<script setup>
// 炉台选择：按炉台号或 IP 搜索，回车选中第一个；选中的炉台全站共用
import { ref, computed, nextTick } from 'vue'
import Icon from './Icon.vue'
import { state, currentFurnace, selectFurnace, dotClass, STATUS_TEXT } from '../store.js'

const open = ref(false)
const query = ref('')
const area = ref('')
const list = ref(null)
const input = ref(null)
const active = ref(0)

const areaLabel = (name) => name === '其他' ? name : name + ' 区'
const groups = computed(() => {
  const byArea = new Map()
  for (const f of state.furnaces) {
    const name = f.area || '其他'
    if (!byArea.has(name)) byArea.set(name, [])
    byArea.get(name).push(f)
  }
  return [...byArea].sort(([a], [b]) => a === '其他' ? 1 : b === '其他' ? -1 : a.localeCompare(b, 'zh-CN', { numeric: true }))
})
const shown = computed(() => {
  const q = query.value.trim().toLowerCase()
  let offset = 0
  return groups.value
    .filter(([name]) => !area.value || area.value === name)
    .map(([name, furnaces]) => {
      const matches = furnaces.filter((f) => f.device_id.toLowerCase().includes(q) || f.device_ip.includes(q))
      const group = { name, matches, offset }
      offset += matches.length
      return group
    })
    .filter((group) => group.matches.length)
})
const matches = computed(() => shown.value.flatMap((group) => group.matches))

function resetActive() {
  active.value = 0
  if (list.value) list.value.scrollTop = 0
}
function chooseArea(name) {
  area.value = name
  resetActive()
  input.value?.focus()
}

function show() {
  open.value = true
  query.value = ''
  area.value = currentFurnace.value ? currentFurnace.value.area || '其他' : ''
  active.value = 0
  nextTick(() => input.value?.focus())
}
function choose(f) {
  if (f) selectFurnace(f.table)
  open.value = false
}
function onKey(e) {
  if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
    e.preventDefault()
    if (!matches.value.length) return
    const next = active.value + (e.key === 'ArrowDown' ? 1 : -1)
    active.value = Math.max(0, Math.min(next, matches.value.length - 1))
    nextTick(() => list.value?.querySelector('.row.on')?.scrollIntoView({ block: 'nearest' }))
  } else if (e.key === 'Enter' && matches.value[active.value]) choose(matches.value[active.value])
  else if (e.key === 'Escape') open.value = false
}
</script>

<template>
  <div class="picker">
    <button class="current" @click="show">
      <template v-if="currentFurnace">
        <span class="dot" :class="dotClass(currentFurnace)"></span>
        <b>{{ currentFurnace.device_id }}</b>
        <span class="muted num">{{ currentFurnace.device_ip }}</span>
      </template>
      <span v-else class="muted">选择炉台（炉台号 / IP）</span>
      <Icon name="search" :size="16" class="search" />
    </button>

    <div v-if="open" class="mask" @click="open = false"></div>
    <div v-if="open" class="pop card">
      <input ref="input" v-model="query" class="input" placeholder="炉台号或 IP，如 A061、172.30.3.61"
        @keydown="onKey" @input="resetActive" />
      <div class="areas">
        <button :class="{ on: !area }" @click="chooseArea('')">全部 {{ state.furnaces.length }}</button>
        <button v-for="[name, furnaces] in groups" :key="name" :class="{ on: area === name }" @click="chooseArea(name)">
          {{ areaLabel(name) }} {{ furnaces.length }}
        </button>
      </div>
      <div ref="list" class="list">
        <section v-for="group in shown" :key="group.name">
          <div class="group-heading">{{ areaLabel(group.name) }} <span class="muted">{{ group.matches.length }} 台</span></div>
          <button v-for="(f, i) in group.matches" :key="f.table" class="row"
            :class="{ on: group.offset + i === active, cur: f.table === state.current }"
            @click="choose(f)" @mouseenter="active = group.offset + i">
            <span class="dot" :class="dotClass(f)"></span>
            <b class="id">{{ f.device_id }}</b>
            <span class="muted num ip">{{ f.device_ip }}</span>
            <span class="st" :class="f.status">{{ STATUS_TEXT[f.status] }}</span>
          </button>
        </section>
        <div v-if="!matches.length" class="none muted">没有匹配的炉台</div>
      </div>
      <div class="foot muted">显示 {{ matches.length }} 台 · ↑↓ 选择，回车确认</div>
    </div>
  </div>
</template>

<style scoped>
.picker { position: relative }
.current { width: 100%; display: flex; align-items: center; gap: 9px; padding: 8px 12px; border: 1px solid var(--line);
  border-radius: 11px; background: #fff; cursor: pointer; text-align: left; box-shadow: 0 1px 2px rgba(15, 23, 42, .04) }
.current:hover { border-color: var(--brand) }
.search { margin-left: auto; color: var(--faint) }
.mask { position: fixed; inset: 0; z-index: 20 }
.pop { position: absolute; top: calc(100% + 6px); left: 0; width: min(460px, 92vw); z-index: 21; padding: 10px }
.areas { display: flex; flex-wrap: wrap; gap: 4px; margin-top: 8px }
.areas button { border: 1px solid var(--line); background: #fff; border-radius: 999px; padding: 3px 10px;
  font-size: 12px; font-weight: 600; color: var(--muted); cursor: pointer }
.areas button.on { background: var(--brand); border-color: var(--brand); color: #fff }
.group-heading { position: sticky; top: 0; z-index: 1; padding: 6px 8px; background: #fff; font-size: 12.5px; font-weight: 700 }
.group-heading .muted { margin-left: 4px; font-weight: 500 }
.list { scroll-padding-top: 30px; max-height: 360px; overflow-y: auto; margin-top: 8px }
.row { width: 100%; display: grid; grid-template-columns: 10px minmax(60px, 1fr) 110px auto; align-items: center; gap: 10px;
  padding: 7px 8px; border: 0; background: none; border-radius: 9px; cursor: pointer; text-align: left }
.row.on { background: #f3f5fb }
.row.cur .id { color: var(--brand) }
.st { justify-self: end; font-size: 12px; color: var(--muted) }
.st.online { color: var(--ok) }
.st.stopped { color: var(--danger) }
.none { padding: 18px; text-align: center }
.foot { font-size: 11.5px; padding: 8px 4px 0; border-top: 1px solid var(--line2); margin-top: 6px }
</style>
