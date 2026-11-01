import os
ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
DATA = os.path.join(ROOT, 'data')
WORK = os.path.join(ROOT, 'work')
import csv, sys, collections
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from rules import BLOCKS
ph={b:v[2] for b,v in BLOCKS.items()}
E=collections.defaultdict(collections.Counter)
for r in csv.DictReader(open(os.path.join(DATA, 'block_edges.tsv')),delimiter='\t'): E[r['from']][r['to']]+=int(r['refs'])
TH=int(sys.argv[1]) if len(sys.argv)>1 else 10
for P in ['P1','P2','P3','P4','P5']:
    nodes=[b for b in BLOCKS if ph[b]==P]
    # Eades-style greedy: repeatedly take node with max (out - in) among remaining within phase -> order; then layer = longest path on forward edges >= TH
    rem=set(nodes); order=[]
    def w(a,b): return E[a][b] if E[a][b]>=TH else 0
    while rem:
        # sinks first (nodes not depending on others) -> we want dependencies first: node whose deps (out-edges) are all placed
        best=max(rem,key=lambda v: sum(w(u,v) for u in rem if u!=v) - sum(w(v,u) for u in rem if u!=v))
        order.append(best); rem.remove(best)
    pos={b:i for i,b in enumerate(order)}
    layer={}
    for b in order:
        deps=[d for d in nodes if d!=b and w(b,d) and pos[d]<pos[b]]
        layer[b]=1+max([layer[d] for d in deps],default=0)
    back=sum(w(b,d) for b in nodes for d in nodes if d!=b and pos[d]>pos[b])
    tot=sum(w(b,d) for b in nodes for d in nodes if d!=b)
    print(f'== {P}  (within-phase refs>={TH}: back {back}/{tot})')
    for L in sorted(set(layer.values())):
        print(f'  L{L}: '+', '.join(b for b in order if layer[b]==L))
