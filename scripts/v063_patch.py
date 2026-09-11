from pathlib import Path

p = Path('src/ui.js')
s = p.read_text()
old = """function fmtEventTime(at) {
  if (!at) return '-';
  const s = String(at);
  const m = s.match(/^(\\d{4})-(\\d{2})-(\\d{2})T(\\d{2}:\\d{2}:\\d{2})/);
  return m ? m[2] + '-' + m[3] + ' ' + m[4] : s;
}"""
new = """function fmtEventTime(at) {
  if (!at) return '-';
  const raw = String(at);
  const d = new Date(raw);
  if (Number.isNaN(d.getTime())) return raw;
  const pad = (n) => String(n).padStart(2, '0');
  return pad(d.getMonth() + 1) + '-' + pad(d.getDate()) + ' ' +
    pad(d.getHours()) + ':' + pad(d.getMinutes()) + ':' + pad(d.getSeconds());
}"""
if old not in s:
    raise SystemExit('fmtEventTime block not found')
s = s.replace(old, new, 1)
old_header = '<th></th><th>时间</th><th>Model</th><th>Policy / Rule</th>'
new_header = '<th></th><th>时间（本地）</th><th>Model</th><th>Policy / Rule</th>'
if old_header not in s:
    raise SystemExit('event time header not found')
s = s.replace(old_header, new_header, 1)
p.write_text(s)

Path('VERSION').write_text('0.6.3\n')

p = Path('CHANGELOG.md')
s = p.read_text()
entry = """## v0.6.3 - 2026-09-10

修正路由记录列表的时间展示。后端与 SQLite 继续统一保存 UTC RFC3339 时间，前端列表改为按浏览器本地时区转换显示。

### 时间显示

- 路由记录列表由直接截取 UTC 字符串改为使用浏览器本地时区。
- 显示格式保持紧凑的 `MM-DD HH:mm:ss`，表头明确标记为“时间（本地）”。
- 鼠标悬停仍保留原始 RFC3339 UTC 时间，方便精确排查和跨时区对照。
- 无效或非标准时间字符串不会报错，直接回退显示原始值。
- 数据库存储、时间筛选和保留策略继续使用 UTC，不改变持久化语义。

### 测试

- 增加 Asia/Shanghai 与 America/Los_Angeles 两个时区的确定性转换检查。
- 保留 Go test、JavaScript syntax、Linux amd64 build、ABI smoke 等标准 CI。

"""
anchor = '# Changelog\n\n'
if anchor not in s:
    raise SystemExit('CHANGELOG anchor missing')
if '## v0.6.3 ' not in s:
    s = s.replace(anchor, anchor + entry, 1)
p.write_text(s)

p = Path('README.md')
s = p.read_text()
s = s.replace('# CPA Key Chain Router v0.6.2', '# CPA Key Chain Router v0.6.3', 1)
section = """## v0.6.3 浏览器本地时间

路由事件在后端和 SQLite 中仍以 UTC RFC3339 保存；管理页“路由记录”的时间列会在浏览器端转换为当前浏览器本地时区，并以 `MM-DD HH:mm:ss` 展示。鼠标悬停保留原始 UTC 时间。这样不同地区访问同一个 CPA 实例时，各自看到符合本地时区的列表时间，同时数据库与筛选逻辑保持统一 UTC 语义。

"""
anchor = '## v0.6.2 SQLite 持久化修复\n'
if anchor not in s:
    raise SystemExit('README v0.6.2 anchor missing')
if '## v0.6.3 浏览器本地时间' not in s:
    s = s.replace(anchor, section + anchor, 1)
s = s.replace('key-chain-router-v0.6.2.so', 'key-chain-router-v0.6.3.so')
p.write_text(s)

print('v0.6.3 patch applied')
