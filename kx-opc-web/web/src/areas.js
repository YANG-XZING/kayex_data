// Area rules are inclusive IPv4 ranges: { from, to, area }.
function ipv4Number(ip) {
  if (typeof ip !== 'string' || !/^(\d{1,3}\.){3}\d{1,3}$/.test(ip)) return null
  const parts = ip.split('.')
  if (parts.some((p) => Number(p) > 255 || (p.length > 1 && p.startsWith('0')))) return null
  return parts.reduce((n, p) => n * 256 + Number(p), 0)
}

export function areaOf(ip, areas, fallback = '其他') {
  const value = ipv4Number(ip)
  if (value === null) return fallback
  for (const rule of areas || []) {
    const from = ipv4Number(rule.from)
    const to = ipv4Number(rule.to)
    if (rule.area && from !== null && to !== null && from <= value && value <= to) return rule.area
  }
  return fallback
}
