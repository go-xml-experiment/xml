// usage: STAX_XML_DIST=/path/to/stax-xml/dist/index.js node reader.mjs FILE WARMUPS RUNS
// stax-xml StreamReaderSync with the same consumer semantics as the Go/Java/Rust benches. In-memory input.
import { readFileSync } from 'node:fs';
import { performance } from 'node:perf_hooks';
const { StreamReaderSync, XmlEventType } = await import(process.env.STAX_XML_DIST ?? 'stax-xml');
const fold = (s, v) => { let n = s; for (let i = 0; i < v.length; i++) n = ((n << 5) - n + v.charCodeAt(i)) | 0; return n; };
const mix = (s, v) => Math.imul((s ^ v) | 0, 16777619) | 0;
function consume(bytes) {
  const r = new StreamReaderSync(bytes, { documentMode: 'document' });
  let events = 0, sum = 0, pending = '';
  const flush = () => { const t = pending.replace(/^[ \t\r\n]+|[ \t\r\n]+$/g, ''); pending = ''; if (t) { events++; sum = fold(fold(sum, 'T'), t); } };
  while (r.next() !== null) {
    const t = r.eventType();
    if (t === XmlEventType.START_ELEMENT) {
      flush(); events++; sum = fold(fold(sum, 'S'), r.name());
      const c = r.attributeCount(); sum = mix(sum, c);
      for (let i = 0; i < c; i++) { sum = fold(sum, r.attributeName(i)); sum = fold(sum, r.attributeValue(i)); }
    } else if (t === XmlEventType.END_ELEMENT) { flush(); events++; sum = fold(fold(sum, 'E'), r.name()); }
    else if (t === XmlEventType.CHARACTERS || t === XmlEventType.CDATA) pending += r.text();
  }
  flush();
  return { events, checksum: sum };
}
const [file, warm, runs] = [process.argv[2], +process.argv[3], +process.argv[4]];
const bytes = readFileSync(file);
for (let i = 0; i < warm; i++) consume(bytes);
// like bench/gobench: each sample repeats the parse for at least 100 ms
const samples = [];
for (let i = 0; i < runs; i++) {
  const t = performance.now(); let n = 0, r;
  while (n === 0 || performance.now() - t < 100) { r = consume(bytes); n++; }
  samples.push({ ...r, seconds: (performance.now() - t) / 1000 / n });
}
console.log(JSON.stringify({ samples }));
