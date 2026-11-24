// 曲线配色：相邻两条对比明显
export const COLORS = ['#4f46e5', '#0ea5e9', '#0ea472', '#f59e0b', '#ef4444', '#8b5cf6', '#14b8a6', '#ec4899', '#64748b', '#84cc16', '#f97316', '#06b6d4']

// 工步号的颜色：同一个工步号在时间线、表格里颜色一致。按整数部分取 12 色里的一个（相邻工步颜色差得开），
// 小数部分（如 24.1、24.2）用同色调变浅区分；手动（0）用灰色，等径（13）固定用主色
const STEP_PALETTE = ['#0ea5e9', '#10b981', '#f59e0b', '#ef4444', '#8b5cf6', '#14b8a6', '#ec4899', '#84cc16', '#f97316', '#06b6d4', '#a855f7', '#eab308']
export function stepColor(code) {
  const tenths = Math.round(Number(code) * 10)
  const main = Math.floor(tenths / 10)
  const sub = tenths % 10
  const base = main === 0 ? '#94a3b8' : main === 13 ? '#4f46e5' : STEP_PALETTE[main % STEP_PALETTE.length]
  return sub ? `color-mix(in srgb, ${base} ${100 - sub * 28}%, white)` : base
}
