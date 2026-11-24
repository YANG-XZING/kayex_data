<script setup>
// 配置信息 → 客户点位配置（内部账户）：勾选客户账户“上位机数据”里能看的点位（实时数据、历史数据、导出都只有这些点位）。
// 存在点位信息表的 customer 列（每次修改追加一行），保存后客户刷新页面即生效。
import { ref, computed, watch } from 'vue'
import { state, postJSON } from '../store.js'

const filter = ref('')
const onlyChecked = ref(false)
const saved = computed(() => new Set(state.columns.map((c) => c.name).filter((p) => state.points[p]?.customer)))
const draft = ref(new Set())
const dirty = computed(() => draft.value.size !== saved.value.size || [...draft.value].some((p) => !saved.value.has(p)))
// 刚加载完或别人改过时，以库里的为准（自己正在改的不覆盖）
let lastSaved = new Set()
watch(saved, (s) => {
  const untouched = draft.value.size === lastSaved.size && [...draft.value].every((p) => lastSaved.has(p))
  if (untouched) draft.value = new Set(s)
  lastSaved = s
}, { immediate: true })

const rows = computed(() => {
  const q = filter.value.trim().toLowerCase()
  return state.columns
    .map((c) => ({ point: c.name, type: c.type, name: state.points[c.name]?.name || '' }))
    .filter((r) => !onlyChecked.value || draft.value.has(r.point))
    .filter((r) => !q || r.point.toLowerCase().includes(q) || r.name.toLowerCase().includes(q))
})

function toggle(p) {
  const s = new Set(draft.value)
  s.has(p) ? s.delete(p) : s.add(p)
  draft.value = s
}
// 勾选 / 取消当前筛选出来的全部点位
function setShown(on) {
  const s = new Set(draft.value)
  for (const r of rows.value) on ? s.add(r.point) : s.delete(r.point)
  draft.value = s
}

const saving = ref(false)
const msg = ref(null) // { ok, text }
async function save() {
  saving.value = true
  msg.value = null
  try {
    const res = await postJSON('/api/points/customer', { points: [...draft.value] })
    state.points = res.points
    msg.value = { ok: true, text: `已保存，客户可见 ${draft.value.size} 个点位（修改了 ${res.changed} 个）` }
  } catch (e) {
    msg.value = { ok: false, text: '保存失败：' + e.message }
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <div class="card intro">
    <div class="row">
      <div class="txt">
        <b>客户点位配置</b>：客户账户的“上位机数据”只显示这里勾选的点位（查看数据、导出数据），其余点位客户看不到。
        已勾选 <b class="num">{{ draft.size }}</b> / {{ state.columns.length }} 个。
      </div>
      <div class="acts">
        <button class="btn" :disabled="!dirty || saving" @click="draft = new Set(saved)">撤销修改</button>
        <button class="btn primary" :disabled="!dirty || saving" @click="save">{{ saving ? '保存中…' : '保存' }}</button>
      </div>
    </div>
    <div v-if="msg" class="msg" :class="{ bad: !msg.ok }">{{ msg.text }}</div>
  </div>

  <div class="card">
    <div class="hd">
      <input v-model="filter" class="input f" placeholder="点位 / 中文名" />
      <label class="only"><input v-model="onlyChecked" type="checkbox" /> 只看已勾选的</label>
      <button class="btn sm" @click="setShown(true)">全选</button>
      <button class="btn sm" @click="setShown(false)">全不选</button>
      <span class="muted cnt">{{ rows.length }} 个</span>
    </div>
    <div class="wrap">
      <table class="grid">
        <thead><tr><th class="ck">客户可见</th><th>点位（OPC tag）</th><th>中文名</th><th>类型</th></tr></thead>
        <tbody>
          <tr v-for="r in rows" :key="r.point" :class="{ on: draft.has(r.point) }" @click="toggle(r.point)">
            <td class="ck"><input type="checkbox" :checked="draft.has(r.point)" @click.stop="toggle(r.point)" /></td>
            <td class="mono">{{ r.point }}</td>
            <td><b v-if="r.name">{{ r.name }}</b><span v-else class="none">未配置中文名</span></td>
            <td class="muted">{{ r.type }}</td>
          </tr>
          <tr v-if="!rows.length"><td colspan="4" class="empty">没有符合条件的点位</td></tr>
        </tbody>
      </table>
    </div>
  </div>
</template>

<style scoped>
.intro { padding: 12px 16px; margin-bottom: 12px; font-size: 13px }
.row { display: flex; align-items: center; gap: 14px; flex-wrap: wrap }
.txt { flex: 1; min-width: 300px; line-height: 1.7 }
.acts { display: flex; gap: 8px; align-items: center }
.msg { margin-top: 8px; font-weight: 600; color: var(--ok) }
.msg.bad { color: var(--danger) }
.hd { gap: 10px }
.f { width: 260px; padding: 6px 10px }
.only { display: flex; align-items: center; gap: 5px; font-weight: 600; font-size: 12.5px }
.btn.sm { padding: 4px 10px; font-size: 12.5px }
.cnt { margin-left: auto; font-size: 12.5px }
.wrap { overflow: auto; max-height: calc(100vh - 300px) }
.mono { font-family: ui-monospace, Consolas, monospace; font-size: 12px }
.none { color: var(--faint); font-size: 12px }
.ck { width: 80px; text-align: center }
tbody tr { cursor: pointer }
tbody tr.on td { background: var(--brand-soft) }
.btn:disabled { opacity: .5; cursor: default }
</style>
