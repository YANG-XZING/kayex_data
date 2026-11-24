<script setup>
import { ref, computed, watch } from 'vue'
import { useRoute } from 'vue-router'
import Icon from './components/Icon.vue'
import LOGO_URL from './logo.js'
import FurnacePicker from './components/FurnacePicker.vue'
import { state, alarmCount, isCustomer, logout, save } from './store.js'

const load = (k, f) => { try { return JSON.parse(localStorage.getItem(k)) ?? f } catch { return f } }

// 上位机记录（kayex_data 的操作记录、报警记录）的两个页面，内部账户和客户都有
const HMI_RECORD_PAGES = {
  record: { to: '/hmi/records/record', label: '操作记录', icon: 'history' },
  alarm: { to: '/hmi/records/alarm', label: '报警记录', icon: 'bell' },
}
// 客户账户：上位机数据（管理员勾选的点位：查看、导出）和上位机记录，没有分组标题（group 为空）
const CUSTOMER_MENU = [
  { items: [
    { key: 'cdata', label: '上位机数据', icon: 'table', children: [
      { to: '/hmi/data', label: '查看数据', icon: 'eye' },
      { to: '/hmi/export', label: '导出数据', icon: 'download' },
    ] },
    { key: 'crec', label: '上位机记录', icon: 'history', children: [HMI_RECORD_PAGES.alarm, HMI_RECORD_PAGES.record] },
  ] },
]
const ADMIN_MENU = [
  // 同级的大分组：OPC 数据（kx_opc）、全量数据（kayex_data）、上位机记录（kayex_data）、硬件信息（sop_hardware）
  // children：可折叠的下级菜单，最多两层（key 用来记住展开状态）
  { group: 'OPC 数据', items: [
    { key: 'curves', label: '曲线显示', icon: 'trend', children: [
      { to: '/curves/live', label: '实时曲线', icon: 'gauge' },
      { key: 'hist', label: '历史曲线', icon: 'history', children: [
        { to: '/curves/history/time', label: '按时间' },
        { to: '/curves/history/crystal', label: '按晶棒' },
      ] },
    ] },
    { key: 'data', label: '数据管理', icon: 'table', children: [
      { to: '/data', label: '查看数据', icon: 'eye' },
      { key: 'export', label: '导出数据', icon: 'download', children: [
        { to: '/export', label: '全量导出' },
        { to: '/crystal', label: '按晶棒导出' },
      ] },
    ] },
    { to: '/status', label: '通讯预警', icon: 'bell', badge: true },
    { key: 'config', label: '配置信息', icon: 'sliders', children: [
      { to: '/points/info', label: '点位信息', icon: 'table' },
      { to: '/points/customer', label: '客户点位配置', icon: 'eye' },
    ] },
  ] },
  // 结构和 OPC 数据的“数据管理 / 导出数据”一致（全量数据没有查看数据页）
  { group: '全量数据', items: [
    { key: 'fdata', label: '数据管理', icon: 'table', children: [
      { key: 'fexport', label: '导出数据', icon: 'download', children: [
        { to: '/extract/time', label: '全量导出' },
        { to: '/extract/crystal', label: '按晶棒导出' },
      ] },
    ] },
  ] },
  // 结构和其他分组一致：分组 / 数据管理 / 子菜单
  { group: '上位机记录', items: [
    { key: 'hmidata', label: '数据管理', icon: 'table', children: [HMI_RECORD_PAGES.record, HMI_RECORD_PAGES.alarm] },
  ] },
  { group: '硬件信息', items: [
    { key: 'hdata', label: '数据管理', icon: 'table', children: [
      { to: '/hardware', label: '导出数据', icon: 'download' },
    ] },
  ] },
]
const MENU = computed(() => (isCustomer.value ? CUSTOMER_MENU : ADMIN_MENU))

const route = useRoute()
// logo 内嵌在 logo.js 里（原图 web/assets-src/logo.png）；万一加载失败显示文字
const logoBad = ref(false)

// 主标题上方的位置提示：分组 / 上级菜单，如“OPC 数据 / 数据管理 / 导出数据”
const crumbs = computed(() => {
  const find = (items, trail) => {
    for (const it of items) {
      if (it.to === route.path) return trail
      if (it.children) {
        const r = find(it.children, [...trail, it.label])
        if (r) return r
      }
    }
    return null
  }
  for (const g of MENU.value) {
    const r = find(g.items, g.group ? [g.group] : [])
    if (r) return r
  }
  return []
})

// ---------- 可折叠菜单 ----------
const leaves = (it) => (it.children ? it.children.flatMap(leaves) : [it.to])
const inGroup = (it) => leaves(it).includes(route.path)
const opened = ref(new Set(load('kx.menuOpen', ['curves', 'hist', 'data', 'export', 'fdata', 'fexport', 'hdata', 'hmidata', 'config', 'cdata', 'crec'])))
const isOpen = (key) => opened.value.has(key)
function toggleOpen(key) {
  const s = new Set(opened.value)
  s.has(key) ? s.delete(key) : s.add(key)
  opened.value = s
  save('kx.menuOpen', [...s])
}
// 打开某个页面时，展开它所在的分组
watch(() => route.path, () => {
  const s = new Set(opened.value)
  const walk = (items) => items.forEach((it) => {
    if (it.children && inGroup(it)) { s.add(it.key); walk(it.children) }
  })
  MENU.value.forEach((g) => walk(g.items))
  if (s.size !== opened.value.size) opened.value = s
}, { immediate: true })
// 侧栏收起时，鼠标移到分组上弹出下级菜单
const flyout = ref(null) // { key, top }
function hover(it, e) {
  if (collapsed.value) flyout.value = { key: it.key, top: e.currentTarget.getBoundingClientRect().top }
}
const collapsed = ref(localStorage.getItem('kx.sidebar') === '1')
function toggle() {
  collapsed.value = !collapsed.value
  save('kx.sidebar', collapsed.value ? 1 : 0)
}


</script>

<template>
  <!-- 登录页整页显示；其余页面带侧栏 -->
  <RouterView v-if="route.meta.bare" />
  <div v-else class="app" :class="{ collapsed }">
    <aside class="side">
      <div class="brand">
        <!-- 公司 logo -->
        <div class="plate">
          <img v-if="!logoBad" :src="LOGO_URL" alt="LINTON" @error="logoBad = true" />
          <span v-else class="wm">LINTON</span>
        </div>
        <div class="brand-text">
          <div class="name">炉台数据平台</div>
        </div>
      </div>

      <nav>
        <section v-for="(g, gi) in MENU" :key="g.group || gi">
          <div v-if="g.group" class="group"><span class="gname">{{ g.group }}</span></div>
          <template v-for="it in g.items" :key="it.label">
            <RouterLink v-if="it.to" :to="it.to" class="item" :title="collapsed ? it.label : ''">
              <span class="ico"><Icon :name="it.icon" :size="17" /></span>
              <span class="label">{{ it.label }}</span>
              <span v-if="it.badge && alarmCount" class="badge">{{ alarmCount }}</span>
            </RouterLink>
            <!-- 可折叠分组：点标题展开 / 收起；侧栏收起时鼠标移上去弹出下级菜单 -->
            <div v-else class="grp" @mouseenter="hover(it, $event)" @mouseleave="flyout = null">
              <button class="item parent" :class="{ on: inGroup(it) }" @click="!collapsed && toggleOpen(it.key)">
                <span class="ico"><Icon :name="it.icon" :size="17" /></span>
                <span class="label">{{ it.label }}</span>
                <span v-if="inGroup(it) && !isOpen(it.key)" class="here"></span>
                <Icon name="chevron" :size="15" class="chev" :class="{ open: isOpen(it.key) }" />
              </button>
              <div v-if="!collapsed" class="fold" :class="{ open: isOpen(it.key) }">
                <div class="inner">
                  <div class="tree">
                    <template v-for="c in it.children" :key="c.label">
                      <RouterLink v-if="c.to" :to="c.to" class="node"><Icon :name="c.icon" :size="15" class="nico" />{{ c.label }}</RouterLink>
                      <template v-else>
                        <button class="node" :class="{ on: inGroup(c) }" @click="toggleOpen(c.key)">
                          <Icon :name="c.icon" :size="15" class="nico" />{{ c.label }}
                          <Icon name="chevron" :size="13" class="chev" :class="{ open: isOpen(c.key) }" />
                        </button>
                        <div class="fold" :class="{ open: isOpen(c.key) }">
                          <div class="inner">
                            <div class="tree l2">
                              <RouterLink v-for="l in c.children" :key="l.to" :to="l.to" class="node leaf">{{ l.label }}</RouterLink>
                            </div>
                          </div>
                        </div>
                      </template>
                    </template>
                  </div>
                </div>
              </div>
              <div v-if="collapsed && flyout?.key === it.key" class="fly" :style="{ top: flyout.top - 6 + 'px' }">
                <div class="fly-in">
                  <div class="fly-title">{{ it.label }}</div>
                  <template v-for="c in it.children" :key="c.label">
                    <RouterLink v-if="c.to" :to="c.to" class="node" @click="flyout = null"><Icon :name="c.icon" :size="15" class="nico" />{{ c.label }}</RouterLink>
                    <template v-else>
                      <div class="fly-sub"><Icon :name="c.icon" :size="14" class="nico" />{{ c.label }}</div>
                      <RouterLink v-for="l in c.children" :key="l.to" :to="l.to" class="node leaf" @click="flyout = null">{{ l.label }}</RouterLink>
                    </template>
                  </template>
                </div>
              </div>
            </div>
          </template>
        </section>
      </nav>

      <div v-if="state.me?.auth" class="user">
        <span class="avatar" :title="collapsed ? state.me.user : ''">{{ state.me.user.slice(0, 1).toUpperCase() }}</span>
        <span class="label uname">{{ state.me.user }}<small>{{ isCustomer ? '客户账户' : '内部账户' }}</small></span>
        <button class="logout" title="退出登录" @click="logout"><Icon name="logout" :size="15" /><span class="label">退出</span></button>
      </div>
      <button class="collapse" @click="toggle">
        <Icon name="menu" :size="16" />
        <span class="label">{{ collapsed ? '' : '收起侧栏' }}</span>
      </button>
    </aside>

    <main class="main">
      <header class="top">
        <div class="ttl">
          <div v-if="crumbs.length" class="crumb">
            <template v-for="(c, i) in crumbs" :key="i"><span v-if="i" class="sep">/</span><span>{{ c }}</span></template>
          </div>
          <h1>{{ route.meta.title }}</h1>
        </div>
        <FurnacePicker v-if="route.meta.opc" class="picker" />
        <RouterLink v-if="alarmCount" to="/status" class="chip danger alarm">{{ alarmCount }} 台炉台通讯异常</RouterLink>
      </header>
      <div v-if="state.furnacesError" class="banner">{{ state.furnacesError }}</div>
      <RouterView />
    </main>
  </div>
</template>

<style scoped>
.app { display: grid; grid-template-columns: 236px minmax(0, 1fr); min-height: 100vh; transition: grid-template-columns .2s ease }
.app.collapsed { grid-template-columns: 72px minmax(0, 1fr) }

/* 深色侧栏 */
.side {
  position: sticky; top: 0; height: 100vh; display: flex; flex-direction: column; overflow: hidden;
  padding: 18px 12px 12px; color: #c7cbe8;
  background:
    radial-gradient(260px 220px at 0% 0%, rgba(99, 102, 241, .35), transparent 70%),
    linear-gradient(180deg, #1b1d3f 0%, #171934 55%, #13152b 100%);
  box-shadow: 4px 0 24px rgba(15, 23, 42, .12);
}
/* 品牌区：logo 放在白底圆角牌上（logo 是深色字，深色侧栏上要有底才看得清），下面是平台名 */
.brand { display: flex; flex-direction: column; align-items: stretch; gap: 12px; padding: 2px 4px 16px; border-bottom: 1px solid rgba(255, 255, 255, .07) }
.plate { height: 52px; border-radius: 13px; background: #fff; display: grid; place-items: center; padding: 8px 14px;
  box-shadow: 0 6px 20px rgba(0, 0, 0, .25), inset 0 -2px 0 rgba(99, 102, 241, .18) }
.plate img { max-width: 100%; max-height: 36px; object-fit: contain; display: block }
.wm { font-weight: 900; font-size: 22px; letter-spacing: 4px; color: #1e40af }
.brand-text { padding: 0 4px }
.brand-text .name { font-weight: 800; font-size: 17px; letter-spacing: 2px; white-space: nowrap; color: #fff }
.brand-text .sub { font-size: 11.5px; color: #8b90bd; white-space: nowrap; margin-top: 3px; letter-spacing: .5px }

nav { flex: 1; overflow-y: auto; overflow-x: hidden; padding-top: 6px; scrollbar-width: thin; scrollbar-color: rgba(255,255,255,.15) transparent }
/* 分组标题：单色加粗字 + 数据库小标签 */
.group { display: flex; align-items: center; gap: 8px; padding: 20px 10px 8px; white-space: nowrap }
.gname { font-size: 13px; font-weight: 800; letter-spacing: 1px; color: #a5abd6 }
.group .db { font-family: ui-monospace, Consolas, monospace; font-size: 10px; font-weight: 600; padding: 0 6px; line-height: 17px;
  border-radius: 5px; color: #8b90bd; border: 1px solid rgba(255, 255, 255, .14) }
.item { position: relative; display: flex; align-items: center; gap: 11px; height: 40px; padding: 0 10px; margin: 2px 0;
  border-radius: 11px; color: #c7cbe8; text-decoration: none; font-weight: 600; white-space: nowrap;
  transition: background .15s, color .15s }
.item:hover { background: rgba(255, 255, 255, .06); color: #fff }
.ico { width: 30px; height: 30px; border-radius: 9px; display: grid; place-items: center; flex: none; color: #9ea3d4;
  transition: background .15s, color .15s }
.item:hover .ico { color: #fff }
.item.router-link-active { color: #fff; background: linear-gradient(90deg, rgba(99, 102, 241, .32), rgba(99, 102, 241, .08)) }
.item.router-link-active::before { content: ''; position: absolute; left: -12px; top: 9px; bottom: 9px; width: 4px;
  border-radius: 0 4px 4px 0; background: #a5b4fc; box-shadow: 0 0 12px rgba(165, 180, 252, .8) }
.item.router-link-active .ico { background: linear-gradient(135deg, #6366f1, #8b5cf6); color: #fff;
  box-shadow: 0 4px 12px rgba(99, 102, 241, .5) }
/* 可折叠分组 */
.grp { position: relative }
.item.parent { width: 100%; border: 0; background: none; cursor: pointer; font: inherit; font-weight: 600; text-align: left }
.item.parent:hover { background: rgba(255, 255, 255, .06) }
.item.parent.on { color: #fff }
.item.parent.on .ico { background: rgba(99, 102, 241, .35); color: #fff }
.chev { margin-left: auto; flex: none; color: #6f74a3; transform: rotate(-90deg); transition: transform .2s ease, color .15s }
.chev.open { transform: none }
.item.parent:hover .chev, .node:hover .chev { color: #c7cbe8 }
.here { width: 6px; height: 6px; border-radius: 50%; background: #a5b4fc; box-shadow: 0 0 8px rgba(165, 180, 252, .9); margin-left: auto }
.here + .chev { margin-left: 8px }
/* 展开 / 收起动画：grid 行高 0fr <-> 1fr，内容多高都能平滑过渡 */
.fold { display: grid; grid-template-rows: 0fr; opacity: 0; transition: grid-template-rows .22s ease, opacity .18s ease }
.fold.open { grid-template-rows: 1fr; opacity: 1 }
.fold > .inner { overflow: hidden; min-height: 0 }
/* 树：左侧一条细引导线，当前页在线上点一个亮点 */
.tree { margin: 2px 0 4px 24px; padding-left: 10px; border-left: 1px solid rgba(255, 255, 255, .09) }
.tree.l2 { margin: 0 0 2px 17px }
.node { position: relative; display: flex; align-items: center; gap: 9px; width: 100%; height: 34px; padding: 0 10px; margin: 1px 0;
  border: 0; border-radius: 9px; background: none; color: #aab0dc; font: inherit; font-weight: 600; font-size: 13px;
  text-decoration: none; white-space: nowrap; cursor: pointer; text-align: left; transition: background .15s, color .15s }
.node:hover { background: rgba(255, 255, 255, .06); color: #fff }
.node.on { color: #fff }
.node.leaf { height: 31px; font-size: 12.5px; font-weight: 500 }
.nico { flex: none; color: #8b90bd }
.node:hover .nico, .node.router-link-active .nico, .node.on .nico { color: #c7d2fe }
.node.router-link-active { color: #fff; font-weight: 700; background: linear-gradient(90deg, rgba(99, 102, 241, .38), rgba(99, 102, 241, .12)) }
.tree > .node.router-link-active::before { content: ''; position: absolute; left: -13.5px; top: 50%; width: 6px; height: 6px; margin-top: -3px;
  border-radius: 50%; background: #a5b4fc; box-shadow: 0 0 8px rgba(165, 180, 252, .9) }
/* 侧栏收起时的弹出菜单 */
.fly { position: fixed; left: 64px; z-index: 60; padding-left: 14px }
.fly-in { min-width: 180px; padding: 8px; border-radius: 13px; background: #23264f; border: 1px solid rgba(255, 255, 255, .09);
  box-shadow: 0 14px 36px rgba(0, 0, 0, .38); animation: pop .14s ease-out }
.fly-title { padding: 4px 10px 6px; font-size: 12px; font-weight: 800; color: #fff; letter-spacing: .5px }
.fly-sub { display: flex; align-items: center; gap: 8px; padding: 8px 10px 2px; font-size: 11.5px; font-weight: 700; color: #8b90bd }
.fly .node.leaf { padding-left: 32px }
@keyframes pop { from { opacity: 0; transform: translateX(-4px) } to { opacity: 1; transform: none } }
.badge { margin-left: auto; min-width: 20px; height: 20px; line-height: 20px; text-align: center; border-radius: 999px;
  padding: 0 6px; font-size: 11.5px; font-weight: 700; background: #ef4444; color: #fff; box-shadow: 0 0 0 3px rgba(239, 68, 68, .18) }

.collapse { display: flex; align-items: center; justify-content: center; gap: 8px; height: 36px; border-radius: 10px; cursor: pointer;
  border: 1px solid rgba(255, 255, 255, .08); background: rgba(255, 255, 255, .04); color: #9ea3d4; font-weight: 600; font-size: 12.5px }
.collapse:hover { color: #fff; background: rgba(255, 255, 255, .08) }

.collapsed .brand { padding-left: 0; padding-right: 0 }
.collapsed .plate { height: 40px; padding: 4px 5px; border-radius: 10px }
.collapsed .brand-text, .collapsed .label, .collapsed .group { display: none }
.collapsed section + section { margin-top: 10px; padding-top: 10px; border-top: 1px solid rgba(255, 255, 255, .07) }
.collapsed .item { justify-content: center; padding: 0 }
.collapsed .item.parent .chev, .collapsed .here { display: none }
.collapsed .badge { position: absolute; top: 1px; right: 4px; min-width: 16px; height: 16px; line-height: 16px; font-size: 10px; padding: 0 4px }
.user { display: flex; align-items: center; gap: 9px; margin: 10px 0 8px; padding: 10px 4px 4px 6px; white-space: nowrap; border-top: 1px solid rgba(255, 255, 255, .07) }
.avatar { width: 28px; height: 28px; flex: none; border-radius: 50%; display: grid; place-items: center; font-weight: 800; font-size: 13px;
  color: #fff; background: linear-gradient(135deg, #6366f1, #4f46e5); box-shadow: 0 0 0 2px rgba(255, 255, 255, .12) }
.uname { display: flex; flex-direction: column; min-width: 0; color: #e0e3f7; font-weight: 700; font-size: 13px; line-height: 1.2; overflow: hidden }
.uname small { color: #8b90bd; font-weight: 600; font-size: 11px }
.logout { margin-left: auto; display: inline-flex; align-items: center; gap: 4px; height: 28px; padding: 0 9px; border-radius: 8px; cursor: pointer;
  border: 1px solid rgba(255, 255, 255, .1); background: rgba(255, 255, 255, .04); color: #c7cbe8; font-weight: 600; font-size: 12px }
.logout:hover { color: #fff; background: rgba(239, 68, 68, .25); border-color: rgba(239, 68, 68, .4) }
.collapsed .user { flex-direction: column; padding: 0; gap: 6px }
.collapsed .logout { margin-left: 0; padding: 0 7px }

.main { min-width: 0; padding: 16px 24px 40px }
.top { display: flex; align-items: center; gap: 18px; margin-bottom: 16px; flex-wrap: wrap }
/* 主标题：左侧竖条 + 上方小字位置提示 + 大字 */
.ttl { position: relative; padding-left: 14px }
.ttl::before { content: ''; position: absolute; left: 0; top: 4px; bottom: 4px; width: 4px; border-radius: 2px; background: var(--brand) }
.crumb { font-size: 11.5px; font-weight: 700; color: var(--muted); letter-spacing: .5px; line-height: 1.2; white-space: nowrap }
.crumb .sep { margin: 0 6px; color: #cbd5e1 }
.top h1 { font-size: 24px; margin: 1px 0 0; font-weight: 800; white-space: nowrap; letter-spacing: 1px; line-height: 1.25; color: #1e1b4b }
.picker { flex: 0 1 420px }
.alarm { margin-left: auto; text-decoration: none; padding: 5px 12px }
.banner { margin-bottom: 12px; padding: 9px 12px; border-radius: 10px; background: var(--danger-soft); color: var(--danger);
  border: 1px solid #f6caca; font-size: 13px }

@media (max-width: 900px) {
  .app, .app.collapsed { grid-template-columns: 72px minmax(0, 1fr) }
  .brand-text, .label, .group { display: none }
  .item { justify-content: center; padding: 0 }
  .fold, .chev, .here { display: none }
}
</style>
