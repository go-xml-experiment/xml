#!/bin/sh
# Writer comparison on the six inputs (plan 7.E writer row): Go xmltext Encoder
# (default and LegacyOutput, scalar build and GOEXPERIMENT=simd build),
# encoding/xml Encoder, Woodstox XMLStreamWriter, quick-xml Writer. Each writes
# the same in-memory event list (loaded outside the timed region) to memory;
# every output is re-read with the strict xmltext reader and must give the
# input's events and checksum. Wrap in tools/performance_lock.py.
# usage: bench/crosswriter.sh OUTDIR [runs] [warmups]
# env:   XML_BENCH_STAX=path to the 16 MiB file; JAVA_HOME
set -e
cd "$(dirname "$0")"
OUT=${1:?outdir}; RUNS=${2:-10}; WARM=${3:-3}
mkdir -p "$OUT/classes" "$OUT/wout"
M2=$HOME/.m2/repository
CP=$M2/com/fasterxml/woodstox/woodstox-core/6.7.0/woodstox-core-6.7.0.jar:$M2/org/codehaus/woodstox/stax2-api/4.2.2/stax2-api-4.2.2.jar
JAVA=${JAVA_HOME:+$JAVA_HOME/bin/}java; JAVAC=${JAVA_HOME:+$JAVA_HOME/bin/}javac
$JAVAC -cp "$CP" -d "$OUT/classes" java/WriterBench.java
(cd .. && go build -o "$OUT/gw" ./bench/gowriter && GOEXPERIMENT=simd go build -o "$OUT/gw-simd" ./bench/gowriter)
(cd rust && CARGO_TARGET_DIR="$OUT/rust-target" cargo build --release --offline -q)
set -- corpus/*MiB.xml
if [ -n "${XML_BENCH_STAX:-}" ]; then set -- "$@" "$XML_BENCH_STAX"; fi
for f in "$@"; do
  b=$(basename "$f" .xml)
  for e in xmltext xmltext-legacy std; do "$OUT/gw" $e "$f" "$WARM" "$RUNS" "$OUT/wout/$e-$b.xml" > "$OUT/$e-$b.json"; done
  for e in xmltext xmltext-legacy; do "$OUT/gw-simd" $e "$f" "$WARM" "$RUNS" "$OUT/wout/$e-simd-$b.xml" > "$OUT/$e-simd-$b.json"; done
  "$OUT/rust-target/release/qx_writer" "$f" "$WARM" "$RUNS" "$OUT/wout/quick-xml-$b.xml" > "$OUT/quick-xml-$b.json"
  $JAVA -cp "$CP:$OUT/classes" WriterBench "$f" "$WARM" "$RUNS" "$OUT/wout/woodstox-$b.xml" > "$OUT/woodstox-$b.json"
  for e in xmltext xmltext-legacy std quick-xml woodstox xmltext-simd xmltext-legacy-simd; do
    "$OUT/gw" verify "$f" "$OUT/wout/$e-$b.xml" > /dev/null || { echo "verify failed: $e $b" >&2; exit 1; }
  done
done
python3 - "$OUT" "$@" <<'PY'
import json,os,statistics,sys
out,files=sys.argv[1],sys.argv[2:]
cols=[('quick-xml','quick-xml'),('xmltext','xmltext'),('xmltext SIMD','xmltext-simd'),('xmltext legacy bytes','xmltext-legacy'),('Woodstox','woodstox'),('encoding/xml','std')]
print('| input | '+' | '.join(c for c,_ in cols)+' |'); print('|---|'+'---:|'*len(cols))
for f in files:
    b=os.path.basename(f)[:-4]; row=[]
    for _,k in cols:
        d=json.load(open(os.path.join(out,f'{k}-{b}.json')))
        t=statistics.median(s['seconds'] for s in d['samples'])
        row.append(f"{t*1e3:.3f} ms / {d['inputBytes']/1048576/t:,.0f}")
    print(f'| {b} | '+' | '.join(row)+' |')
PY
