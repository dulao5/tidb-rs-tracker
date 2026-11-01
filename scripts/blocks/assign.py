import os
ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
DATA = os.path.join(ROOT, 'data')
WORK = os.path.join(ROOT, 'work')
import csv, re, sys, collections, os
sys.path.insert(0, os.path.dirname(__file__))
from rules import BLOCKS, RULES
TR = DATA
rules = [(b, re.compile(f), re.compile(s) if s else None) for b, f, s in RULES]
for b, _, _ in RULES: assert b in BLOCKS, b
def assign(file, sym):
    for b, f, s in rules:
        if f.search(file) and (s is None or s.search(sym)): return b
    return None
def main():
  rows = list(csv.DictReader(open(TR + '/go_funcs.tsv'), delimiter='\t'))
  out = []; size = collections.Counter(); nf = collections.Counter(); files = collections.defaultdict(set)
  for r in rows:
      b = assign(r['file'], r['symbol'])
      out.append((r['pkg'], r['file'], r['symbol'], r['start_line'], b))
      if r['generated'] == '0':
          size[b] += int(r['lines']); nf[b] += 1; files[b].add(r['file'])
  with open(os.path.join(DATA, 'func_blocks.tsv'), 'w') as w:
      w.write('pkg\tfile\tsymbol\tstart_line\tblock\n')
      for o in sorted(out): w.write('\t'.join(o) + '\n')
  with open(os.path.join(DATA, 'blocks.tsv'), 'w') as w:
    w.write('block\tdomain\ttitle\tphase\n')
    for b in sorted(BLOCKS): w.write('\t'.join((b,) + BLOCKS[b]) + '\n')
  ph = collections.Counter()
  for b, (d, t, p) in BLOCKS.items(): ph[p] += size[b]
  print('by phase (function lines, non-generated):', dict(ph))
  print(f"{'block':8} {'phase':5} {'lines':>6} {'funcs':>5} {'files':>5} title")
  for b, (d, t, p) in BLOCKS.items():
      print(f"{b:8} {p:5} {size[b]:6} {nf[b]:5} {len(files[b]):5} {t[:60]}")

if __name__ == "__main__":
    main()
