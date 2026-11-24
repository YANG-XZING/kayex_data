<script setup>
// 线条图标（24×24 视图，描边），只放用到的几个
defineProps({ name: String, size: { type: Number, default: 18 } })
const PATHS = {
  gauge: 'M12 13l3-4M5.6 18a8 8 0 1 1 12.8 0M12 13a1 1 0 1 0 0 .01',
  trend: 'M3 17l5-6 4 4 8-9M15 6h5v5',
  history: 'M12 8v4l3 2M3.05 11a9 9 0 1 1 .5 4M3 4v5h5',
  bell: 'M10 5a2 2 0 1 1 4 0a7 7 0 0 1 4 6v3a4 4 0 0 0 2 3H4a4 4 0 0 0 2-3v-3a7 7 0 0 1 4-6M9 17v1a3 3 0 0 0 6 0v-1',
  table: 'M4 5h16v14H4zM4 10h16M10 10v9',
  cpu: 'M7 7h10v10H7zM10 10h4v4h-4zM9 3v4M15 3v4M9 17v4M15 17v4M3 9h4M3 15h4M17 9h4M17 15h4',
  sliders: 'M4 6h8M16 6h4M4 12h2M10 12h10M4 18h11M19 18h1M14 4v4M8 10v4M17 16v4',
  menu: 'M4 6h16M4 12h16M4 18h16',
  search: 'M10 17a7 7 0 1 0 0-14a7 7 0 0 0 0 14zM21 21l-6-6',
  zoomin: 'M10 17a7 7 0 1 0 0-14a7 7 0 0 0 0 14zM21 21l-6-6M10 7v6M7 10h6',
  star: 'M12 17.75l-6.17 3.24l1.18-6.87l-5-4.86l6.9-1l3.09-6.26l3.09 6.26l6.9 1l-5 4.86l1.18 6.87z',
  x: 'M18 6L6 18M6 6l12 12',
  chevron: 'M6 9l6 6l6-6',
  eye: 'M2 12s3.6-7 10-7s10 7 10 7s-3.6 7-10 7S2 12 2 12zM12 15a3 3 0 1 0 0-6a3 3 0 0 0 0 6z',
  download: 'M12 4v11M7 10l5 5l5-5M5 20h14',
  logout: 'M15 4h3a2 2 0 0 1 2 2v12a2 2 0 0 1-2 2h-3M10 17l-5-5l5-5M5 12h11',
  folder: 'M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z',
}
</script>

<template>
  <svg :width="size" :height="size" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"
    stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path :d="PATHS[name]" /></svg>
</template>
