#!/bin/sh
# Scalar build vs GOEXPERIMENT=simd build of the same Go code (reader with the
# full-value consumer, and writer), six inputs, interleaved runs. On arm64 the
# simd build adds the NEON kernels in xmltext/kernel_simd_arm64.go; on other
# platforms it is identical to the scalar build. Wrap in performance_lock.py.
# usage: bench/simdmatrix.sh OUTDIR [rounds]   (env XML_BENCH_STAX = 16 MiB file)
set -e
cd "$(dirname "$0")/.."
OUT=${1:?outdir}; ROUNDS=${2:-3}
mkdir -p "$OUT"
go build -o "$OUT/gobench-scalar" ./bench/gobench
go build -o "$OUT/gowriter-scalar" ./bench/gowriter
GOEXPERIMENT=simd go build -o "$OUT/gobench-simd" ./bench/gobench
GOEXPERIMENT=simd go build -o "$OUT/gowriter-simd" ./bench/gowriter
set -- bench/corpus/*MiB.xml
if [ -n "${XML_BENCH_STAX:-}" ]; then set -- "$@" "$XML_BENCH_STAX"; fi
r=1
while [ $r -le "$ROUNDS" ]; do
  for f in "$@"; do
    b=$(basename "$f" .xml)
    for v in scalar simd; do
      "$OUT/gobench-$v" xmltext "$f" 3 10 memory > "$OUT/reader-$v-$b-$r.json"
      "$OUT/gobench-$v" xmltext-reader "$f" 3 10 memory > "$OUT/reader-stream-$v-$b-$r.json"
      "$OUT/gowriter-$v" xmltext "$f" 3 10 > "$OUT/writer-$v-$b-$r.json"
    done
  done
  r=$((r+1))
done
python3 - "$OUT" "$ROUNDS" "$@" <<'PY'
import json,os,statistics,sys
out,rounds,files=sys.argv[1],int(sys.argv[2]),sys.argv[3:]
def med(p): return statistics.median(s['seconds'] for s in json.load(open(p))['samples'])
print('| input | reader bytes scalar | reader bytes SIMD | SIMD/scalar | Reader scalar | Reader SIMD | SIMD/scalar | writer scalar | writer SIMD | SIMD/scalar |')
print('|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|')
for f in files:
    b=os.path.basename(f)[:-4]; row=[b]
    for kind in ('reader','reader-stream','writer'):
        sc=[med(os.path.join(out,f'{kind}-scalar-{b}-{r}.json')) for r in range(1,rounds+1)]
        sm=[med(os.path.join(out,f'{kind}-simd-{b}-{r}.json')) for r in range(1,rounds+1)]
        ratios=[y/x for x,y in zip(sc,sm)]
        row+= [f"{statistics.median(sc)*1e3:.3f} ms",f"{statistics.median(sm)*1e3:.3f} ms",f"{min(ratios):.2f}-{max(ratios):.2f}"]
    print('| '+' | '.join(row)+' |')
PY
