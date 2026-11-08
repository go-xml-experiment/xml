#!/bin/sh
# Cross-runtime reader comparison on the six inputs (plan 7.E): Go xmltext and
# encoding/xml, Woodstox (via run.sh), quick-xml (bench/rust) and stax-xml on
# Node (bench/node). All consumers fold the same events into the same checksum;
# a mismatch aborts. In-memory input. Wrap in tools/performance_lock.py.
# usage: bench/crossrt.sh OUTDIR [runs] [warmups]
# env:   XML_BENCH_STAX=path to the original 16 MiB file
#        STAX_XML_DIST=path to stax-xml/dist/index.js (omit to skip Node)
set -e
cd "$(dirname "$0")"
OUT=${1:?outdir}; RUNS=${2:-10}; WARM=${3:-3}
mkdir -p "$OUT"
sh run.sh "$RUNS" "$WARM" > "$OUT/go-java.txt"
(cd rust && CARGO_TARGET_DIR="$OUT/rust-target" cargo build --release --offline -q)
set -- corpus/*MiB.xml
if [ -n "${XML_BENCH_STAX:-}" ]; then set -- "$@" "$XML_BENCH_STAX"; fi
for f in "$@"; do
  b=$(basename "$f" .xml)
  "$OUT/rust-target/release/qx_bench" "$f" "$WARM" "$RUNS" > "$OUT/rust-$b.json"
  if [ -n "${STAX_XML_DIST:-}" ]; then node node/reader.mjs "$f" "$WARM" "$RUNS" > "$OUT/node-$b.json"; fi
done
python3 - "$OUT" "$@" <<'PY'
import json,os,re,statistics,sys
out,files=sys.argv[1],sys.argv[2:]
ms={}
for l in open(os.path.join(out,'go-java.txt')):
    m=re.match(r'(\S+)\.xml\s+(\S+)\s+median\s+([\d.]+) ms.*events (\d+) checksum (-?\d+)',l)
    if m: ms.setdefault(m[1],{})[m[2]]=(float(m[3]),m[4],m[5])
cols=[('quick-xml','rust'),('xmltext','xmltext'),('xmltext Reader','xmltext-reader'),('Woodstox','woodstox'),('stax-xml','node'),('encoding/xml','legacy')]
print('| input | '+' | '.join(c for c,_ in cols)+' |'); print('|---|'+'---:|'*len(cols))
for f in files:
    b=os.path.basename(f)[:-4]; sz=os.path.getsize(f)
    want=ms[b]['woodstox'][1:]
    for rt in ('rust','node'):
        p=os.path.join(out,f'{rt}-{b}.json')
        if os.path.exists(p):
            s=json.load(open(p))['samples']
            if any((str(x['events']),str(x['checksum']))!=want for x in s): sys.exit(f'{rt} checksum mismatch on {b}')
            ms[b][rt]=(statistics.median(x['seconds'] for x in s)*1e3,)
    cells=[f"{ms[b][k][0]:.3f} ms / {sz/1048576/(ms[b][k][0]/1e3):,.0f}" if k in ms[b] else '-' for _,k in cols]
    print(f'| {b} | '+' | '.join(cells)+' |')
PY
