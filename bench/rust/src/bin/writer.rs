// quick-xml Writer: input read once into an event list (not timed), then written to memory.
use quick_xml::events::{BytesEnd, BytesStart, BytesText, Event};
use quick_xml::{Reader, Writer};
use std::time::Instant;

enum Ev {
    Start(String, Vec<(String, String)>),
    End(String),
    Text(String),
}

fn load(bytes: &[u8]) -> Vec<Ev> {
    let mut r = Reader::from_reader(bytes);
    let mut out = Vec::new();
    let mut text = String::new();
    let flush = |text: &mut String, out: &mut Vec<Ev>| {
        if !text.is_empty() {
            out.push(Ev::Text(std::mem::take(text)));
        }
    };
    loop {
        match r.read_event().unwrap() {
            Event::Start(e) => {
                flush(&mut text, &mut out);
                out.push(start(&e));
            }
            Event::Empty(e) => {
                flush(&mut text, &mut out);
                out.push(start(&e));
                out.push(Ev::End(String::from_utf8(e.local_name().as_ref().to_vec()).unwrap()));
            }
            Event::End(e) => {
                flush(&mut text, &mut out);
                out.push(Ev::End(String::from_utf8(e.local_name().as_ref().to_vec()).unwrap()));
            }
            Event::Text(e) => text.push_str(&e.decode().unwrap()),
            Event::GeneralRef(e) => {
                if let Some(c) = e.resolve_char_ref().unwrap() {
                    text.push(c);
                } else {
                    text.push(match e.as_ref() {
                        b"amp" => '&',
                        b"lt" => '<',
                        b"gt" => '>',
                        b"quot" => '"',
                        b"apos" => '\'',
                        o => panic!("entity {:?}", o),
                    });
                }
            }
            Event::CData(e) => text.push_str(&e.decode().unwrap()),
            Event::Eof => break,
            _ => {}
        }
    }
    flush(&mut text, &mut out);
    out
}

fn start(e: &BytesStart) -> Ev {
    let attrs = e
        .attributes()
        .map(|a| {
            let a = a.unwrap();
            (
                String::from_utf8(a.key.local_name().as_ref().to_vec()).unwrap(),
                a.normalized_value(quick_xml::XmlVersion::Implicit1_0).unwrap().into_owned(),
            )
        })
        .collect();
    Ev::Start(String::from_utf8(e.local_name().as_ref().to_vec()).unwrap(), attrs)
}

fn write(evs: &[Ev], sink: &mut Vec<u8>) {
    sink.clear();
    let mut w = Writer::new(&mut *sink);
    for ev in evs {
        match ev {
            Ev::Start(n, attrs) => {
                let mut s = BytesStart::new(n.as_str());
                for (k, v) in attrs {
                    s.push_attribute((k.as_str(), v.as_str()));
                }
                w.write_event(Event::Start(s)).unwrap();
            }
            Ev::End(n) => w.write_event(Event::End(BytesEnd::new(n.as_str()))).unwrap(),
            Ev::Text(t) => w.write_event(Event::Text(BytesText::new(t))).unwrap(),
        }
    }
}

fn main() {
    let a: Vec<String> = std::env::args().collect();
    let bytes = std::fs::read(&a[1]).unwrap();
    let (warm, runs): (usize, usize) = (a[2].parse().unwrap(), a[3].parse().unwrap());
    let evs = load(&bytes);
    let mut sink = Vec::with_capacity(2 * bytes.len());
    write(&evs, &mut sink);
    let out_bytes = sink.len();
    if a.len() > 4 {
        std::fs::write(&a[4], &sink).unwrap();
    }
    let t0 = Instant::now();
    let mut i = 0;
    while i < warm || t0.elapsed().as_secs_f64() < 1.0 {
        write(&evs, &mut sink);
        i += 1;
    }
    let mut samples = Vec::new();
    for _ in 0..runs {
        let t = Instant::now();
        let mut n = 0u32;
        while n == 0 || t.elapsed().as_millis() < 100 {
            write(&evs, &mut sink);
            n += 1;
        }
        samples.push(format!("{{\"seconds\":{:.9},\"iterations\":{}}}", t.elapsed().as_secs_f64() / n as f64, n));
    }
    println!(
        "{{\"engine\":\"quick-xml\",\"inputBytes\":{},\"outputBytes\":{},\"events\":{},\"samples\":[{}]}}",
        bytes.len(),
        out_bytes,
        evs.len(),
        samples.join(",")
    );
}
