import java.io.ByteArrayOutputStream;
import java.io.File;
import java.io.FileOutputStream;
import java.io.OutputStream;
import javax.xml.stream.XMLOutputFactory;
import javax.xml.stream.XMLStreamWriter;

/**
 * The stax-xml cross-runtime writer workload (12,798 generated books) with Woodstox.
 * cold RECORDS OUTFILE       one run to a file, as in stax-xml's harness (includes JIT warm-up)
 * steady RECORDS WARM RUNS   repeated runs into memory; prints JSON with per-run seconds
 */
public final class StaxWriterBench {
  private static final String DESCRIPTION =
      "Lorem ipsum dolor sit amet, consectetur adipiscing elit, sed do eiusmod tempor incididunt ut labore et dolore magna aliqua. "
          + "Ut enim ad minim veniam, quis nostrud exercitation ullamco laboris nisi ut aliquip ex ea commodo consequat. "
          + "Duis aute irure dolor in reprehenderit in voluptate velit esse cillum dolore eu fugiat nulla pariatur.";
  private static final String[] CHAPTERS = {"Introduction", "Main Content", "Conclusion"};

  static void generate(OutputStream out, int records) throws Exception {
    XMLStreamWriter writer = XMLOutputFactory.newFactory().createXMLStreamWriter(out, "UTF-8");
    writer.writeStartDocument("UTF-8", "1.0");
    writer.writeStartElement("books");
    for (int id = 1; id <= records; id++) {
      writer.writeStartElement("book");
      writer.writeAttribute("id", "book-" + id);
      element(writer, "title", "Sample Book Title Number " + id + " - Lorem ipsum dolor sit amet, consectetur adipiscing elit");
      element(writer, "author", "Author Name " + id);
      element(writer, "isbn", "978" + (100000000 + ((id * 48271) % 900000000)));
      element(writer, "publisher", "Sample Publisher " + id);
      element(writer, "publishDate", String.format("%d-%02d-%02d", 2020 + id % 5, id % 12 + 1, id % 28 + 1));
      element(writer, "description", DESCRIPTION);
      writer.writeStartElement("chapters");
      for (int c = 1; c <= 3; c++) {
        writer.writeStartElement("chapter");
        writer.writeAttribute("number", Integer.toString(c));
        writer.writeCharacters(CHAPTERS[c - 1] + " Chapter for Book " + id);
        writer.writeEndElement();
      }
      writer.writeEndElement();
      writer.writeEndElement();
    }
    writer.writeEndElement();
    writer.writeEndDocument();
    writer.close();
  }

  static void element(XMLStreamWriter w, String name, String value) throws Exception {
    w.writeStartElement(name);
    w.writeCharacters(value);
    w.writeEndElement();
  }

  public static void main(String[] args) throws Exception {
    int records = Integer.parseInt(args[1]);
    if (args[0].equals("cold")) {
      long t = System.nanoTime();
      File f = new File(args[2]);
      generate(new FileOutputStream(f), records);
      double s = (System.nanoTime() - t) / 1e9;
      System.out.printf("{\"records\":%d,\"bytes\":%d,\"seconds\":%.9f}%n", records, f.length(), s);
      return;
    }
    int warm = Integer.parseInt(args[2]), runs = Integer.parseInt(args[3]);
    ByteArrayOutputStream sink = new ByteArrayOutputStream(12 << 20);
    long start = System.nanoTime();
    for (int i = 0; i < warm || System.nanoTime() - start < 2_000_000_000L; i++) { sink.reset(); generate(sink, records); }
    StringBuilder secs = new StringBuilder();
    for (int r = 0; r < runs; r++) {
      long t = System.nanoTime();
      int n = 0;
      while (n == 0 || System.nanoTime() - t < 200_000_000L) { sink.reset(); generate(sink, records); n++; }
      if (r > 0) secs.append(',');
      secs.append((System.nanoTime() - t) / 1e9 / n);
    }
    System.out.println("{\"engine\":\"woodstox\",\"mode\":\"steady\",\"bytes\":" + sink.size() + ",\"seconds\":[" + secs + "]}");
  }
}
