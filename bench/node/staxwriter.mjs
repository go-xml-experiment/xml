// The stax-xml cross-runtime writer workload with stax-xml's WriterSyncSink.
// usage: STAX_XML_DIST=... node staxwriter.mjs cold RECORDS OUTFILE | steady RECORDS WARM RUNS
import { closeSync, openSync, statSync, writeSync } from 'node:fs';
import { performance } from 'node:perf_hooks';
const { WriterSyncSink } = await import(process.env.STAX_XML_DIST ?? 'stax-xml');
const description = 'Lorem ipsum dolor sit amet, consectetur adipiscing elit, sed do eiusmod tempor incididunt ut labore et dolore magna aliqua. Ut enim ad minim veniam, quis nostrud exercitation ullamco laboris nisi ut aliquip ex ea commodo consequat. Duis aute irure dolor in reprehenderit in voluptate velit esse cillum dolore eu fugiat nulla pariatur.';
function writeBook(writer, bookId) {
  writer.writeStartElement('book', { attributes: { id: `book-${bookId}` } });
  const element = (name, value) => { writer.writeStartElement(name); writer.writeCharacters(value); writer.writeEndElement(); };
  element('title', `Sample Book Title Number ${bookId} - Lorem ipsum dolor sit amet, consectetur adipiscing elit`);
  element('author', `Author Name ${bookId}`);
  element('isbn', `978${100000000 + ((bookId * 48271) % 900000000)}`);
  element('publisher', `Sample Publisher ${bookId}`);
  element('publishDate', `${2020 + bookId % 5}-${String(bookId % 12 + 1).padStart(2, '0')}-${String(bookId % 28 + 1).padStart(2, '0')}`);
  element('description', description);
  writer.writeStartElement('chapters');
  for (const [index, name] of ['Introduction', 'Main Content', 'Conclusion'].entries()) {
    writer.writeStartElement('chapter', { attributes: { number: String(index + 1) } });
    writer.writeCharacters(`${name} Chapter for Book ${bookId}`);
    writer.writeEndElement();
  }
  writer.writeEndElement();
  writer.writeEndElement();
}
function generate(sink, records) {
  const writer = new WriterSyncSink(sink, { bufferSize: 64 * 1024, flushThreshold: 0.8 });
  writer.writeStartDocument('1.0', 'utf-8');
  writer.writeStartElement('books');
  for (let id = 1; id <= records; id++) writeBook(writer, id);
  writer.writeEndElement();
  writer.writeEndDocument();
  writer.close();
}
const [mode, records] = [process.argv[2], +process.argv[3]];
if (mode === 'cold') {
  const t = performance.now();
  const fd = openSync(process.argv[4], 'w');
  generate({ write: chunk => writeSync(fd, chunk, undefined, 'utf8'), close: () => closeSync(fd) }, records);
  console.log(JSON.stringify({ records, bytes: statSync(process.argv[4]).size, seconds: (performance.now() - t) / 1000 }));
} else {
  const [warm, runs] = [+process.argv[4], +process.argv[5]];
  let bytes = 0;
  const sink = { write: chunk => { bytes += typeof chunk === 'string' ? chunk.length : chunk.byteLength; }, close: () => {} };
  const t0 = performance.now();
  for (let i = 0; i < warm || performance.now() - t0 < 2000; i++) { bytes = 0; generate(sink, records); }
  const secs = [];
  for (let r = 0; r < runs; r++) {
    const t = performance.now(); let n = 0;
    while (n === 0 || performance.now() - t < 200) { bytes = 0; generate(sink, records); n++; }
    secs.push((performance.now() - t) / 1000 / n);
  }
  console.log(JSON.stringify({ engine: 'stax-xml', mode: 'steady', bytes, seconds: secs }));
}
