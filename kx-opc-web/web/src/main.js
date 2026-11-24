import { createApp } from 'vue'
import { createRouter, createWebHashHistory } from 'vue-router'
import 'uplot/dist/uPlot.min.css'
import './style.css'
import App from './App.vue'
import CurvesPage from './views/CurvesPage.vue'
import FurnaceData from './views/FurnaceData.vue'
import OpcExport from './views/OpcExport.vue'
import Status from './views/Status.vue'
import PointInfo from './views/PointInfo.vue'
import CustomerPoints from './views/CustomerPoints.vue'
import Extract from './views/Extract.vue'
import Crystal from './views/Crystal.vue'
import HmiRecords from './views/HmiRecords.vue'
import Login from './views/Login.vue'
import { state, loadMe, canVisit, homeOf, setUnauthorizedHandler, start } from './store.js'

// meta.opc：顶部显示炉台选择框（数据按炉台查）；全量数据导出来自 kayex_data，页面里自己选设备。
// meta.bare：整页显示，没有侧栏（登录页）。客户账户只能打开上位机数据、上位机记录（store.canVisit）
const router = createRouter({
  history: createWebHashHistory(),
  routes: [
    { path: '/', redirect: () => homeOf(state.me) },
    { path: '/login', component: Login, meta: { title: '登录', bare: true, public: true } },
    { path: '/curves', redirect: '/curves/live' },
    { path: '/curves/live', component: CurvesPage, meta: { title: '实时曲线', opc: true, curve: 'live' } },
    { path: '/curves/history', redirect: (to) => ({ path: '/curves/history/time', query: to.query }) },
    { path: '/curves/history/time', component: CurvesPage, meta: { title: '按时间', opc: true, curve: 'history', hist: 'time' } },
    { path: '/curves/history/crystal', component: CurvesPage, meta: { title: '按晶棒', opc: true, curve: 'history', hist: 'crystal' } },
    { path: '/data', component: FurnaceData, meta: { title: '查看数据', opc: true } },
    { path: '/export', component: OpcExport, meta: { title: '全量导出', opc: true } },
    { path: '/crystal', component: Crystal, meta: { title: '按晶棒导出', opc: true } },
    { path: '/status', component: Status, meta: { title: '通讯预警' } },
    { path: '/extract/time', component: Extract, meta: { title: '全量导出', extract: 'time' } },
    { path: '/extract/crystal', component: Extract, meta: { title: '按晶棒导出', extract: 'crystal' } },
    { path: '/hardware', component: Extract, meta: { title: '导出数据' } },
    // 配置信息：属于 OPC 数据（kx_opc 的点位），但不需要选炉台
    { path: '/points', redirect: (to) => (to.query.tab === 'customer' ? '/points/customer' : '/points/info') },
    { path: '/points/info', component: PointInfo, meta: { title: '点位信息' } },
    { path: '/points/customer', component: CustomerPoints, meta: { title: '客户点位配置' } },
    // 上位机数据（客户账户，查看数据页只有客户可见的点位）、上位机记录（两种账户都有）
    { path: '/hmi/data', component: FurnaceData, meta: { title: '查看数据', opc: true } },
    { path: '/hmi/export', component: OpcExport, meta: { title: '导出数据', opc: true } },
    { path: '/hmi/records', redirect: '/hmi/records/record' },
    { path: '/hmi/records/record', component: HmiRecords, meta: { title: '操作记录', opc: true, hmi: 'record' } },
    { path: '/hmi/records/alarm', component: HmiRecords, meta: { title: '报警记录', opc: true, hmi: 'alarm' } },
    // 旧地址
    { path: '/realtime', redirect: '/data' },
    { path: '/history', redirect: (to) => ({ path: '/data', query: { ...to.query, tab: 'history' } }) },
    { path: '/trend', redirect: '/curves/live' },
    { path: '/fields', redirect: '/points/info' },
    { path: '/extract', redirect: '/extract/time' },
  ],
})
// 未登录去登录页（登录后回来）；客户账户打开别的页面时回到它的首页。第一次进来先问后端是谁
let checked = false
router.beforeEach(async (to) => {
  if (!checked) { checked = true; await loadMe() }
  if (to.meta.public) return state.me && to.path === '/login' ? homeOf(state.me) : true
  if (!state.me) return { path: '/login', query: to.fullPath !== '/' ? { redirect: to.fullPath } : {} }
  start()
  if (!canVisit(state.me, to.path)) return homeOf(state.me)
  return true
})
// 登录过期：回到登录页
setUnauthorizedHandler(() => router.replace({ path: '/login', query: { redirect: router.currentRoute.value.fullPath } }))
router.afterEach((to) => {
  document.title = `${to.meta.title || ''} · 炉台数据平台`
})

createApp(App).use(router).mount('#app')
