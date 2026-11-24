<script setup>
// 全量数据导出的设备选择：清单来自 kayex_data（PLC 超级表的 device_id / device_ip 标签），
// 和顶部 OPC 监控的炉台清单（kx_opc）不是同一份。按区分组显示全部设备，可按设备号、IP 搜索，
// 也可以直接输入清单里没有的 IP。
import { ref, computed, nextTick } from 'vue'
import Icon from './Icon.vue'

const props = defineProps({
  modelValue: String,
  devices: Array, // [{ id, ip }]
  areas: Array, // [{ prefix, area }]，网段前缀 -> 区号
  loading: Boolean,
})
const emit = defineEmits(['update:modelValue'])

const open = ref(false)
const query = ref('')
const area = ref('') // 只看某个区，空为全部
const input = ref(null)

const areaOf = (ip) => (props.areas || []).find((a) => ip.startsWith(a.prefix))?.area || '其他'
const current = computed(() => props.devices.find((d) => d.ip === props.modelValue))
// 按区分组，区的顺序与配置一致，“其他”放最后
const groups = computed(() => {
  const order = [...new Set((props.areas || []).map((a) => a.area)), '其他']
  const m = new Map(order.map((a) => [a, []]))
  for (const d of props.devices) m.get(areaOf(d.ip)).push(d)
  return order.map((a) => ({ area: a, list: m.get(a) })).filter((g) => g.list.length)
})
const shown = computed(() => {
  const q = query.value.trim().toLowerCase()
  return groups.value
    .filter((g) => !area.value || g.area === area.value)
    .map((g) => ({ ...g, list: q ? g.list.filter((d) => d.id.toLowerCase().includes(q) || d.ip.includes(q)) : g.list }))
    .filter((g) => g.list.length)
})
const shownCount = computed(() => shown.value.reduce((n, g) => n + g.list.length, 0))
// 输入的是完整 IP、但清单里没有：允许直接用
const rawIp = computed(() => {
  const q = query.value.trim()
  return /^\d+\.\d+\.\d+\.\d+$/.test(q) && !props.devices.some((d) => d.ip === q) ? q : ''
})

function show() {
  open.value = true
  query.value = ''
  nextTick(() => input.value?.focus())
}
function choose(ip) {
  if (ip) emit('update:modelValue', ip)
  open.value = false
}
function onKey(e) {
  if (e.key === 'Enter') choose(shown.value[0]?.list[0]?.ip || rawIp.value)
  else if (e.key === 'Escape') open.value = false
}
</script>

<template>
  <div class="dp">
    <button class="cur" @click="show">
      <template v-if="modelValue">
        <b>{{ current?.id || '清单外' }}</b><span class="muted num">{{ modelValue }}</span>
      </template>
      <span v-else class="muted">选择设备（设备号 / IP）</span>
      <Icon name="search" :size="15" class="ic" />
    </button>
    <div v-if="open" class="mask" @click="open = false"></div>
    <div v-if="open" class="pop card">
      <input ref="input" v-model="query" class="input" placeholder="设备号或 IP，如 BB012、10.65.111.12；回车选第一个" @keydown="onKey" />
      <div class="areas">
        <button :class="{ on: !area }" @click="area = ''">全部 <span>{{ devices.length }}</span></button>
        <button v-for="g in groups" :key="g.area" :class="{ on: area === g.area }" @click="area = g.area">
          {{ g.area === '其他' ? '其他' : g.area + ' 区' }} <span>{{ g.list.length }}</span></button>
      </div>
      <div class="list">
        <button v-if="rawIp" class="dev raw" @click="choose(rawIp)"><b>使用 {{ rawIp }}</b><span class="muted">不在清单里</span></button>
        <section v-for="g in shown" :key="g.area">
          <div class="gh">{{ g.area === '其他' ? '其他' : g.area + ' 区' }}<span class="muted">{{ g.list.length }} 台</span></div>
          <div class="devs">
            <button v-for="d in g.list" :key="d.ip" class="dev" :class="{ sel: d.ip === modelValue }" :title="d.ip" @click="choose(d.ip)">
              <b>{{ d.id }}</b><span class="muted num">{{ d.ip }}</span>
            </button>
          </div>
        </section>
        <div v-if="!shownCount && !rawIp" class="none muted">{{ loading ? '读取设备清单…' : '没有匹配的设备' }}</div>
      </div>
      <div class="foot muted">kayex_data 共 {{ devices.length }} 台<template v-if="query || area">，当前显示 {{ shownCount }} 台</template></div>
    </div>
  </div>
</template>

<style scoped>
.dp { position: relative; min-width: 240px }
.cur { width: 100%; display: flex; align-items: center; gap: 9px; padding: 7px 12px; border: 1px solid var(--line);
  border-radius: 10px; background: #fff; cursor: pointer; text-align: left }
.cur:hover { border-color: var(--brand) }
.ic { margin-left: auto; color: var(--faint) }
.mask { position: fixed; inset: 0; z-index: 20 }
.pop { position: absolute; top: calc(100% + 6px); left: 0; width: min(640px, 92vw); z-index: 21; padding: 10px }
.areas { display: flex; flex-wrap: wrap; gap: 4px; margin-top: 8px }
.areas button { border: 1px solid var(--line); background: #fff; border-radius: 999px; padding: 3px 10px; font-size: 12px; font-weight: 600;
  color: var(--muted); cursor: pointer }
.areas button span { opacity: .7; margin-left: 2px }
.areas button.on { background: var(--brand); border-color: var(--brand); color: #fff }
.list { max-height: 380px; overflow-y: auto; margin-top: 8px }
.gh { position: sticky; top: 0; background: #fff; font-weight: 700; font-size: 12.5px; padding: 6px 2px 4px; display: flex; gap: 8px; align-items: baseline; z-index: 1 }
.gh .muted { font-weight: 500; font-size: 11.5px }
.devs { display: grid; grid-template-columns: repeat(auto-fill, minmax(140px, 1fr)); gap: 4px; margin-bottom: 6px }
.dev { display: flex; flex-direction: column; align-items: flex-start; gap: 1px; padding: 5px 8px; border: 1px solid var(--line2); background: #fff;
  border-radius: 8px; cursor: pointer; text-align: left; font-size: 12.5px }
.dev .muted { font-size: 11px }
.dev:hover { border-color: var(--brand); background: #f6f7ff }
.dev.sel { border-color: var(--brand); background: var(--brand-soft) }
.dev.raw { width: 100%; flex-direction: row; gap: 8px; margin-bottom: 6px }
.none { padding: 18px; text-align: center }
.foot { font-size: 11.5px; padding: 8px 4px 0; border-top: 1px solid var(--line2); margin-top: 6px }
</style>
