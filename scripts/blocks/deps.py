import os
ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
DATA = os.path.join(ROOT, 'data')
WORK = os.path.join(ROOT, 'work')
import csv, re, sys, os, collections
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from rules import BLOCKS, RULES
from assign import assign
TR = DATA
fb = {}
for r in csv.DictReader(open(os.path.join(DATA, 'func_blocks.tsv')), delimiter='\t'):
    fb[(r['file'], r['symbol'], r['start_line'])] = r['block']; fb.setdefault((r['file'], r['symbol']), r['block'])
for r in csv.DictReader(open(TR + '/go_decls.tsv'), delimiter='\t'):
    fb.setdefault((r['file'], r['name']), assign(r['file'], r['name']))
W = collections.Counter()
for r in csv.DictReader(open(os.path.join(WORK, 'go_refs.tsv')), delimiter='\t'):
    a = fb.get((r['from_file'], r['from_symbol'], r['from_start_line']))
    b = fb.get((r['to_file'], r['to_symbol'])) or assign(r['to_file'], r['to_symbol'])
    if a and b and a != b: W[(a, b)] += 1
ph = {b: v[2] for b, v in BLOCKS.items()}
with open(os.path.join(DATA, 'block_edges.tsv'), 'w') as w:
    w.write('from\tto\trefs\n')
    for (a, b), n in sorted(W.items()): w.write(f'{a}\t{b}\t{n}\n')
