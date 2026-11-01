import os
ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
DATA = os.path.join(ROOT, 'data')
WORK = os.path.join(ROOT, 'work')
import csv, sys, collections, subprocess, re, os
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from rules import BLOCKS
TR = DATA
lines={}; 
for r in csv.DictReader(open(TR+'/go_funcs.tsv'),delimiter='\t'):
    if r['generated']=='0': lines[(r['file'],r['symbol'],r['start_line'])]=int(r['lines'])
size=collections.Counter(); files=collections.defaultdict(set)
for r in csv.DictReader(open(os.path.join(DATA, 'func_blocks.tsv')),delimiter='\t'):
    k=(r['file'],r['symbol'],r['start_line'])
    if k in lines: size[r['block']]+=lines[k]; files[r['block']].add(r['file'])
out=subprocess.run([sys.executable,os.path.join(os.path.dirname(os.path.abspath(__file__)),'layers.py'),'10'],capture_output=True,text=True).stdout
layer={}; P=None
for l in out.splitlines():
    m=re.match(r'  L(\d+): (.*)',l)
    if m:
        for b in m.group(2).split(', '): layer[b]=int(m.group(1))
ph={b:v[2] for b,v in BLOCKS.items()}
order={'DONE':0,'P1':1,'P2':2,'P3':3,'P4':4,'P5':5}
E=collections.defaultdict(collections.Counter)
for r in csv.DictReader(open(os.path.join(DATA, 'block_edges.tsv')),delimiter='\t'): E[r['from']][r['to']]+=int(r['refs'])
def stubs(b):
    xs=[(t,n) for t,n in E[b].items() if (ph.get(t) not in order or order[ph[t]]>order[ph[b]]) and n>=10 and t!='D00.3']
    xs.sort(key=lambda x:-x[1])
    return ', '.join(f'{t}({n})' for t,n in xs[:4])
for P_ in ['P1','P2','P3','P4','P5']:
    bs=sorted([b for b in BLOCKS if ph[b]==P_],key=lambda b:(layer.get(b,9),b))
    tot=sum(size[b] for b in bs)
    print(f'\n### {P_}（{len(bs)} 块，Go 函数体 {tot/1000:.1f}k 行）\n')
    print('| 层 | 块 | 内容 | Go 行数 | 文件数 | 需要边界桩 / 接口的依赖（引用数 ≥ 10） |')
    print('|---|---|---|--:|--:|---|')
    for b in bs:
        print(f'| L{layer.get(b,"-")} | {b} | {BLOCKS[b][1]} | {size[b]:,} | {len(files[b])} | {stubs(b) or "—"} |')
print('\n### 不在 P1–P5 的块\n')
print('| 块 | 状态 | 内容 | Go 行数 |'); print('|---|---|---|--:|')
for b,(d,t,p) in BLOCKS.items():
    if p not in ('P1','P2','P3','P4','P5'): print(f'| {b} | {p} | {t} | {size[b]:,} |')
