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
