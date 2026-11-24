// 曲线用的点位选择，实时曲线和历史曲线共用一份（记在浏览器里）
import { ref, computed, watch } from 'vue'
import { state, trendColumns, save } from './store.js'
import { COLORS } from './colors.js'

const saved = JSON.parse(localStorage.getItem('kx.curveCols') || 'null')
const selected = ref(Array.isArray(saved) ? saved : [])
watch(trendColumns, (cols) => {
  if (!selected.value.length && cols.length) selected.value = cols.slice(0, 4)
}, { immediate: true })

export function useCurveCols() {
  const maxTags = computed(() => state.settings.history_max_tags)
  const cols = computed(() => selected.value.filter((c) => trendColumns.value.includes(c)).slice(0, maxTags.value))
  const boolCols = computed(() => new Set(state.columns.filter((c) => c.type === 'BOOL').map((c) => c.name)))
  // 给一组点位配颜色（按顺序），画图用
  const seriesOf = (list) => list.map((c, i) => ({ col: c, color: COLORS[i % COLORS.length], bool: boolCols.value.has(c) }))
  const series = computed(() => seriesOf(cols.value))
  function setSelected(list) {
    selected.value = list
    save('kx.curveCols', list)
  }
  return { selected, cols, series, seriesOf, setSelected, maxTags }
}

// 曲线的值：数值原样，开关量 0/1，其他为 null
export const toNum = (v) => (typeof v === 'number' ? v : typeof v === 'boolean' ? +v : null)
