#!/bin/sh
# The stax-xml writer workload (12,798 books, ~10.3 MiB) on every runtime in two modes:
#  cold   one process, one run, file sink  (what stax-xml's BENCHMARK.md reports; includes
#         JIT warm-up for Java and Node and the cost of generating the records)
#  steady the same code repeated in one process into memory after warm-up
# Plus Go "gen" (record generation alone) to show how much of the time is not the writer.
# usage: bench/staxwriter.sh OUTDIR   (env JAVA_HOME, STAX_XML_DIST)
set -e
cd "$(dirname "$0")"
OUT=${1:?outdir}; N=12798
mkdir -p "$OUT/classes"
M2=$HOME/.m2/repository
CP=$M2/com/fasterxml/woodstox/woodstox-core/6.7.0/woodstox-core-6.7.0.jar:$M2/org/codehaus/woodstox/stax2-api/4.2.2/stax2-api-4.2.2.jar
JAVA=${JAVA_HOME:+$JAVA_HOME/bin/}java; JAVAC=${JAVA_HOME:+$JAVA_HOME/bin/}javac
$JAVAC -cp "$CP" -d "$OUT/classes" java/StaxWriterBench.java
(cd .. && go build -o "$OUT/gws" ./bench/gowriterstax && GOEXPERIMENT=simd go build -o "$OUT/gws-simd" ./bench/gowriterstax)
(cd rust && CARGO_TARGET_DIR="$OUT/rust-target" cargo build --release --offline -q)
for i in 1 2 3 4 5; do
  "$OUT/gws" cold $N "$OUT/go.xml" xmltext > "$OUT/cold-xmltext-$i.json"
  "$OUT/gws" cold $N "$OUT/go-legacy.xml" xmltext-legacy > "$OUT/cold-xmltext-legacy-$i.json"
  "$OUT/gws" cold $N "$OUT/go-std.xml" std > "$OUT/cold-std-$i.json"
  "$OUT/rust-target/release/qx_staxwriter" cold $N "$OUT/rust.xml" > "$OUT/cold-quick-xml-$i.json"
  $JAVA -cp "$CP:$OUT/classes" StaxWriterBench cold $N "$OUT/java.xml" > "$OUT/cold-woodstox-$i.json"
  if [ -n "${STAX_XML_DIST:-}" ]; then node node/staxwriter.mjs cold $N "$OUT/node.xml" > "$OUT/cold-stax-xml-$i.json"; fi
done
"$OUT/gws" steady $N 3 5 xmltext > "$OUT/steady-xmltext.json"
"$OUT/gws-simd" steady $N 3 5 xmltext > "$OUT/steady-xmltext-simd.json"
"$OUT/gws" steady $N 3 5 xmltext-legacy > "$OUT/steady-xmltext-legacy.json"
"$OUT/gws" steady $N 3 5 std > "$OUT/steady-std.json"
"$OUT/gws" gen $N 3 5 > "$OUT/steady-go-generation-only.json"
"$OUT/rust-target/release/qx_staxwriter" steady $N 3 5 > "$OUT/steady-quick-xml.json"
$JAVA -cp "$CP:$OUT/classes" StaxWriterBench steady $N 3 5 > "$OUT/steady-woodstox.json"
if [ -n "${STAX_XML_DIST:-}" ]; then node node/staxwriter.mjs steady $N 3 5 > "$OUT/steady-stax-xml.json"; fi
python3 - "$OUT" <<'PY'
import json,os,statistics,sys
out=sys.argv[1]
names=[('quick-xml','quick-xml'),('xmltext','xmltext'),('xmltext-legacy','xmltext-legacy'),('std','encoding/xml'),('woodstox','Woodstox'),('stax-xml','stax-xml (Node)')]
mib=10775015/1048576
print('| runtime | cold, 5 processes (median) | steady state, after warm-up |'); print('|---|---:|---:|')
for k,label in names:
    cs=[json.load(open(os.path.join(out,f'cold-{k}-{i}.json')))['seconds'] for i in range(1,6) if os.path.exists(os.path.join(out,f'cold-{k}-{i}.json'))]
    sp=os.path.join(out,f'steady-{k}.json')
    cold=f"{statistics.median(cs)*1e3:.1f} ms / {mib/statistics.median(cs):,.0f} MiB/s" if cs else '-'
    st='-'
    if os.path.exists(sp):
        s=statistics.median(json.load(open(sp))['seconds']); st=f"{s*1e3:.1f} ms / {mib/s:,.0f} MiB/s"
    print(f'| {label} | {cold} | {st} |')
for extra,label in (('xmltext-simd','xmltext, GOEXPERIMENT=simd'),('go-generation-only','Go record generation alone (no writer)')):
    s=statistics.median(json.load(open(os.path.join(out,f'steady-{extra}.json')))['seconds'])
    print(f'| {label} | - | {s*1e3:.1f} ms / {mib/s:,.0f} MiB/s |')
PY
