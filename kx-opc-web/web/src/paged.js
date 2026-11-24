// 分页读取的大表格（历史数据、上位机记录共用）：先取第一页（带总行数），表格滚动到哪一段再取哪一段；
// 一个月的数据也不用整体读进浏览器。只缓存最近取过的若干页。
import { ref } from 'vue'

const PAGE = 2000
const KEEP_PAGES = 50

// fetchPage(offset, limit) 返回接口结果（第一页带 total）；返回 { total, loading, error, ms, run, row, clear }
export function usePaged(fetchPage) {
  const total = ref(0)
  const loading = ref(false)
  const error = ref('')
  const ms = ref(0)
  const version = ref(0) // 有新页到达时加一，表格据此重画
  let pages = new Map()
  let inflight = new Set()
  let gen = 0

  function clear() {
    gen++
    pages = new Map()
    inflight = new Set()
    total.value = 0
    loading.value = false
    version.value++
  }

  // 重新查询：取第一页；返回第一页的结果（失败为 null，错误在 error 里）
  async function run() {
    clear()
    const g = gen
    loading.value = true
    error.value = ''
    const t0 = performance.now()
    try {
      const d = await fetchPage(0, PAGE)
      if (g !== gen) return null
      total.value = d.total || 0
      pages.set(0, d)
      ms.value = Math.round(performance.now() - t0)
      version.value++
      return d
    } catch (e) {
      if (g === gen) error.value = '查询失败：' + e.message
      return null
    } finally {
      if (g === gen) loading.value = false
    }
  }

  async function load(p) {
    if (inflight.has(p)) return
    inflight.add(p)
    const g = gen
    try {
      const d = await fetchPage(p * PAGE, PAGE)
      if (g !== gen) return
      pages.set(p, d)
      while (pages.size > KEEP_PAGES) pages.delete(pages.keys().next().value)
      version.value++
    } catch (e) {
      if (g === gen) error.value = '读取失败：' + e.message
    } finally {
      inflight.delete(p)
    }
  }

  // 第 r 行所在的页和页内序号；这一页还没取到时开始取，并返回 null（表格先显示占位）
  function row(r) {
    void version.value
    const p = Math.floor(r / PAGE)
    const d = pages.get(p)
    if (!d) {
      load(p)
      return null
    }
    return { d, i: r - p * PAGE }
  }

  return { total, loading, error, ms, run, row, clear }
}
