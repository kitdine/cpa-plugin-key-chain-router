from pathlib import Path

# Make the snapshot test exercise the weighted explanation path rather than the
# single-candidate shortcut.
p = Path('scripts/review_findings_patch.py')
s = p.read_text()
old = '''        Candidates: []*PolicyCandidate{{ID: "c1", Name: "A", AuthIndex: "idx", Provider: "codex", Priority: 100, Weight: 3, Enabled: true}},\n'''
new = '''        Candidates: []*PolicyCandidate{\n            {ID: "c1", Name: "A", AuthIndex: "idx", Provider: "codex", Priority: 100, Weight: 3, Enabled: true},\n            {ID: "c2", Name: "B", AuthIndex: "idx-b", Provider: "codex", Priority: 100, Weight: 1, Enabled: true},\n        },\n'''
if old not in s:
    raise SystemExit('snapshot-test candidate pattern not found')
p.write_text(s.replace(old, new, 1))

# newSQLiteSinkV4 is the canonical successful-open point. Mark health there so
# direct constructor users/tests observe a healthy source as well; restart may
# call recordSQLiteOpenV62 again harmlessly.
p = Path('scripts/review_findings_patch.py')
s = p.read_text()
old = '''\ts := &sqliteSink{db: db, path: path, ch: make(chan RoutingEvent, 1024), stop: make(chan struct{}), done: make(chan struct{}), cfg: cfg}\n\tgo s.loop()\n\treturn s, nil\n}\n'''
new = '''\ts := &sqliteSink{db: db, path: path, ch: make(chan RoutingEvent, 1024), stop: make(chan struct{}), done: make(chan struct{}), cfg: cfg}\n\tgo s.loop()\n\trecordSQLiteOpenV62(path)\n\treturn s, nil\n}\n'''
if old not in s:
    raise SystemExit('sqlite constructor return pattern not found')
p.write_text(s.replace(old, new, 1))
