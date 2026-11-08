#!/bin/sh
# Same-session reference comparison (plan 7.E): Woodstox vs xmltext vs legacy
# encoding/xml, same consumer checksum, in-memory input.
# usage: bench/run.sh [runs] [warmups]
# XML_BENCH_STAX appends the original external corpus; XML_BENCH_OUT saves
# raw samples and verifies cross-engine event counts/checksums (relative to bench/).
set -e
cd "$(dirname "$0")"
RUNS=${1:-10}; WARM=${2:-3}
M2=$HOME/.m2/repository
CP=$M2/com/fasterxml/woodstox/woodstox-core/6.7.0/woodstox-core-6.7.0.jar:$M2/org/codehaus/woodstox/stax2-api/4.2.2/stax2-api-4.2.2.jar
OUT=${TMPDIR:-/tmp}/xmlbench; mkdir -p "$OUT/classes"
javac -cp "$CP" -d "$OUT/classes" java/ReaderBench.java
(cd .. && go build -o "$OUT/gobench" ./bench/gobench)
set -- corpus/*MiB.xml
if [ -n "${XML_BENCH_STAX:-}" ]; then set -- "$@" "$XML_BENCH_STAX"; fi
for f in "$@"; do
  for e in "java woodstox" "go xmltext" "go xmltext-reader" "go xmltext-reader-stream" "go legacy"; do
    set -- $e
    if [ "$1" = java ]; then
      j=$(java -cp "$CP:$OUT/classes" ReaderBench woodstox "$f" $WARM $RUNS memory)
    else
      j=$("$OUT/gobench" "$2" "$f" $WARM $RUNS memory)
    fi
    echo "$j" | python3 -c '
import hashlib,json,pathlib,sys,statistics
d=json.load(sys.stdin); s=sorted(x["seconds"] for x in d["samples"])
if sys.argv[3]:
    root=pathlib.Path(sys.argv[3]); root.mkdir(parents=True,exist_ok=True)
    if sys.argv[2] == "woodstox":
        ref=d
    else:
        ref=json.loads((root/(sys.argv[1]+".woodstox.json")).read_text())
    expected=ref["samples"][0]
    if any((x["events"],x["checksum"]) != (expected["events"],expected["checksum"]) for x in d["samples"]):
        raise SystemExit("cross-engine checksum/event mismatch: "+sys.argv[1]+" "+sys.argv[2])
    d["input"]=str(pathlib.Path(sys.argv[4]).resolve())
    with open(sys.argv[4],"rb") as f: d["sha256"]=hashlib.file_digest(f,"sha256").hexdigest()
    (root/(sys.argv[1]+"."+sys.argv[2]+".json")).write_text(json.dumps(d)+"\n")
print("%-26s %-18s median %8.3f ms  min %8.3f ms  events %d checksum %d" % (sys.argv[1], sys.argv[2], statistics.median(s)*1e3, s[0]*1e3, d["samples"][0]["events"], d["samples"][0]["checksum"]))' "$(basename "$f")" "$2" "${XML_BENCH_OUT:-}" "$f"
  done
done
