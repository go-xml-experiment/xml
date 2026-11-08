// quick-xml reader benchmark with the same consumer semantics as bench/gobench
// and bench/java: Empty events become Start+End, text and attribute values are
// entity-decoded, names/text fold as UTF-16 code units (bytes when ASCII, which
// is identical), whitespace-only text is dropped and the rest trimmed.
// In-memory input.
use quick_xml::events::{BytesStart, Event};
use quick_xml::Reader;
use std::fs;
use std::time::Instant;

#[inline]
fn fold(mut seed: i32, b: &[u8]) -> i32 {
    if b.is_ascii() {
        // same 4-bytes-per-multiply fold as bench/gobench (identical result)
        let mut it = b.chunks_exact(4);
        for c in &mut it {
            seed = seed
                .wrapping_mul(923521)
                .wrapping_add((c[0] as i32).wrapping_mul(29791))
                .wrapping_add((c[1] as i32) * 961)
                .wrapping_add((c[2] as i32) * 31)
                .wrapping_add(c[3] as i32);
        }
        for &c in it.remainder() {
            seed = seed.wrapping_mul(31).wrapping_add(c as i32);
        }
        return seed;
    }
    for u in std::str::from_utf8(b).unwrap().encode_utf16() {
        seed = seed.wrapping_mul(31).wrapping_add(u as i32);
    }
    seed
}
#[inline]
fn mix(seed: i32, n: usize) -> i32 {
    (seed ^ n as i32).wrapping_mul(16777619)
}
#[inline]
fn is_ws(c: u8) -> bool {
    matches!(c, b' ' | b'\t' | b'\r' | b'\n')
}
fn flush(pending: &mut Vec<u8>, events: &mut u64, sum: &mut i32) {
    let s = pending.iter().position(|&c| !is_ws(c)).unwrap_or(pending.len());
    let e = pending.iter().rposition(|&c| !is_ws(c)).map_or(s, |p| p + 1);
    if s < e {
        *events += 1;
        *sum = fold(sum.wrapping_mul(31).wrapping_add('T' as i32), &pending[s..e]);
    }
    pending.clear();
}
fn start(e: &BytesStart, pending: &mut Vec<u8>, events: &mut u64, sum: &mut i32) {
    flush(pending, events, sum);
    *events += 1;
    *sum = fold(sum.wrapping_mul(31).wrapping_add('S' as i32), e.local_name().as_ref());
    let attrs: Vec<_> = e.attributes().map(|a| a.unwrap()).collect();
    *sum = mix(*sum, attrs.len());
    for a in attrs {
        *sum = fold(*sum, a.key.local_name().as_ref());
        if a.value.contains(&b'&') {
            *sum = fold(*sum, a.normalized_value(quick_xml::XmlVersion::Implicit1_0).unwrap().as_bytes());
        } else {
            *sum = fold(*sum, a.value.as_ref());
        }
    }
}
fn end(name: &[u8], pending: &mut Vec<u8>, events: &mut u64, sum: &mut i32) {
    flush(pending, events, sum);
    *events += 1;
    *sum = fold(sum.wrapping_mul(31).wrapping_add('E' as i32), name);
}
fn consume(bytes: &[u8]) -> (u64, i32) {
    let mut r = Reader::from_reader(bytes);
    let (mut events, mut sum) = (0u64, 0i32);
    let mut pending: Vec<u8> = Vec::new();
    loop {
        match r.read_event().unwrap() {
            Event::Start(e) => start(&e, &mut pending, &mut events, &mut sum),
            Event::Empty(e) => {
                start(&e, &mut pending, &mut events, &mut sum);
                end(e.local_name().as_ref(), &mut pending, &mut events, &mut sum);
            }
            Event::End(e) => end(e.local_name().as_ref(), &mut pending, &mut events, &mut sum),
            Event::Text(e) => pending.extend_from_slice(e.as_ref()),
            Event::GeneralRef(e) => {
                if let Some(c) = e.resolve_char_ref().unwrap() {
                    let mut b = [0u8; 4];
                    pending.extend_from_slice(c.encode_utf8(&mut b).as_bytes());
                } else {
                    pending.push(match e.as_ref() {
                        b"amp" => b'&',
                        b"lt" => b'<',
                        b"gt" => b'>',
                        b"quot" => b'"',
                        b"apos" => b'\'',
                        other => panic!("unknown entity {:?}", other),
                    });
                }
            }
            Event::CData(e) => pending.extend_from_slice(e.as_ref()),
            Event::Eof => break,
            _ => {}
        }
    }
    flush(&mut pending, &mut events, &mut sum);
    (events, sum)
}
fn main() {
    let a: Vec<String> = std::env::args().collect();
    let bytes = fs::read(&a[1]).unwrap();
    let (warm, runs): (usize, usize) = (a[2].parse().unwrap(), a[3].parse().unwrap());
    for _ in 0..warm {
        consume(&bytes);
    }
    // like bench/gobench: each sample repeats the parse for at least 100 ms
    let mut out = Vec::new();
    for _ in 0..runs {
        let t = Instant::now();
        let mut n = 0u32;
        let (mut e, mut c) = (0, 0);
        while n == 0 || t.elapsed().as_millis() < 100 {
            (e, c) = consume(&bytes);
            n += 1;
        }
        out.push(format!("{{\"events\":{e},\"checksum\":{c},\"seconds\":{:.9}}}", t.elapsed().as_secs_f64() / n as f64));
    }
    println!("{{\"samples\":[{}]}}", out.join(","));
}
