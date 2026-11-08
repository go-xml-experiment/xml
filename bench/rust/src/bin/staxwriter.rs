// The stax-xml cross-runtime writer workload with quick-xml.
// cold RECORDS OUTFILE | steady RECORDS WARM RUNS (memory sink, JSON with per-run seconds)
use quick_xml::events::{BytesDecl, BytesEnd, BytesStart, BytesText, Event};
use quick_xml::Writer;
use std::io::{BufWriter, Write};
use std::time::Instant;

const DESCRIPTION: &str = "Lorem ipsum dolor sit amet, consectetur adipiscing elit, sed do eiusmod tempor incididunt ut labore et dolore magna aliqua. Ut enim ad minim veniam, quis nostrud exercitation ullamco laboris nisi ut aliquip ex ea commodo consequat. Duis aute irure dolor in reprehenderit in voluptate velit esse cillum dolore eu fugiat nulla pariatur.";

fn element<W: Write>(writer: &mut Writer<W>, name: &str, value: &str) {
    writer.write_event(Event::Start(BytesStart::new(name))).unwrap();
    writer.write_event(Event::Text(BytesText::new(value))).unwrap();
    writer.write_event(Event::End(BytesEnd::new(name))).unwrap();
}

fn generate<W: Write>(mut writer: Writer<W>, records: usize) -> W {
    writer.write_event(Event::Decl(BytesDecl::new("1.0", Some("UTF-8"), None))).unwrap();
    writer.write_event(Event::Start(BytesStart::new("books"))).unwrap();
    for book_id in 1..=records {
        let id = format!("book-{book_id}");
        let mut book = BytesStart::new("book");
        book.push_attribute(("id", id.as_str()));
        writer.write_event(Event::Start(book)).unwrap();
        element(&mut writer, "title", &format!("Sample Book Title Number {book_id} - Lorem ipsum dolor sit amet, consectetur adipiscing elit"));
        element(&mut writer, "author", &format!("Author Name {book_id}"));
        element(&mut writer, "isbn", &format!("978{}", 100000000 + ((book_id * 48271) % 900000000)));
        element(&mut writer, "publisher", &format!("Sample Publisher {book_id}"));
        element(&mut writer, "publishDate", &format!("{}-{:02}-{:02}", 2020 + book_id % 5, book_id % 12 + 1, book_id % 28 + 1));
        element(&mut writer, "description", DESCRIPTION);
        writer.write_event(Event::Start(BytesStart::new("chapters"))).unwrap();
        for (index, name) in ["Introduction", "Main Content", "Conclusion"].iter().enumerate() {
            let mut chapter = BytesStart::new("chapter");
            let number = (index + 1).to_string();
            chapter.push_attribute(("number", number.as_str()));
            writer.write_event(Event::Start(chapter)).unwrap();
            writer.write_event(Event::Text(BytesText::new(&format!("{name} Chapter for Book {book_id}")))).unwrap();
            writer.write_event(Event::End(BytesEnd::new("chapter"))).unwrap();
        }
        writer.write_event(Event::End(BytesEnd::new("chapters"))).unwrap();
        writer.write_event(Event::End(BytesEnd::new("book"))).unwrap();
    }
    writer.write_event(Event::End(BytesEnd::new("books"))).unwrap();
    writer.into_inner()
}

fn main() {
    let a: Vec<String> = std::env::args().collect();
    let records: usize = a[2].parse().unwrap();
    if a[1] == "cold" {
        let t = Instant::now();
        let f = std::fs::File::create(&a[3]).unwrap();
        let mut out = generate(Writer::new(BufWriter::new(f)), records);
        out.flush().unwrap();
        let s = t.elapsed().as_secs_f64();
        let bytes = std::fs::metadata(&a[3]).unwrap().len();
        println!("{{\"records\":{records},\"bytes\":{bytes},\"seconds\":{s:.9}}}");
        return;
    }
    let (warm, runs): (usize, usize) = (a[3].parse().unwrap(), a[4].parse().unwrap());
    let mut sink: Vec<u8> = Vec::with_capacity(12 << 20);
    let mut once = |sink: &mut Vec<u8>| {
        let mut v = std::mem::take(sink);
        v.clear();
        *sink = generate(Writer::new(v), records);
    };
    let t0 = Instant::now();
    let mut i = 0;
    while i < warm || t0.elapsed().as_secs_f64() < 1.0 {
        once(&mut sink);
        i += 1;
    }
    let mut secs = Vec::new();
    for _ in 0..runs {
        let t = Instant::now();
        let mut n = 0u32;
        while n == 0 || t.elapsed().as_millis() < 200 {
            once(&mut sink);
            n += 1;
        }
        secs.push(format!("{:.9}", t.elapsed().as_secs_f64() / n as f64));
    }
    println!("{{\"engine\":\"quick-xml\",\"mode\":\"steady\",\"bytes\":{},\"seconds\":[{}]}}", sink.len(), secs.join(","));
}
