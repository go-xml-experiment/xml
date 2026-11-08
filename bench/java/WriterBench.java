import com.ctc.wstx.stax.WstxInputFactory;
import com.ctc.wstx.stax.WstxOutputFactory;
import java.io.ByteArrayInputStream;
import java.io.ByteArrayOutputStream;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.ArrayList;
import java.util.List;
import javax.xml.stream.XMLInputFactory;
import javax.xml.stream.XMLOutputFactory;
import javax.xml.stream.XMLStreamConstants;
import javax.xml.stream.XMLStreamReader;
import javax.xml.stream.XMLStreamWriter;

/** Woodstox XMLStreamWriter: input read once into an event list (not timed), then written to memory. */
public final class WriterBench {
  record Event(int type, String name, String[] attrs, String text) {}

  static List<Event> load(byte[] bytes) throws Exception {
    XMLInputFactory f = new WstxInputFactory();
    f.setProperty(XMLInputFactory.SUPPORT_DTD, false);
    f.setProperty(XMLInputFactory.IS_REPLACING_ENTITY_REFERENCES, true);
    XMLStreamReader r = f.createXMLStreamReader(new ByteArrayInputStream(bytes));
    List<Event> out = new ArrayList<>();
    while (r.hasNext()) {
      int t = r.next();
      if (t == XMLStreamConstants.START_ELEMENT) {
        int n = r.getAttributeCount();
        String[] a = new String[2 * n];
        for (int i = 0; i < n; i++) {
          a[2 * i] = r.getAttributeLocalName(i);
          a[2 * i + 1] = r.getAttributeValue(i);
        }
        out.add(new Event(1, r.getLocalName(), a, null));
      } else if (t == XMLStreamConstants.END_ELEMENT) {
        out.add(new Event(2, r.getLocalName(), null, null));
      } else if (t == XMLStreamConstants.CHARACTERS || t == XMLStreamConstants.CDATA) {
        out.add(new Event(3, null, null, r.getText()));
      }
    }
    return out;
  }

  static void write(XMLOutputFactory f, List<Event> evs, ByteArrayOutputStream sink) throws Exception {
    sink.reset();
    XMLStreamWriter w = f.createXMLStreamWriter(sink, "UTF-8");
    for (Event e : evs) {
      switch (e.type) {
        case 1 -> {
          w.writeStartElement(e.name);
          for (int i = 0; i < e.attrs.length; i += 2) w.writeAttribute(e.attrs[i], e.attrs[i + 1]);
        }
        case 2 -> w.writeEndElement();
        default -> w.writeCharacters(e.text);
      }
    }
    w.flush();
    w.close();
  }

  public static void main(String[] args) throws Exception {
    Path path = Path.of(args[0]);
    int warmups = Integer.parseInt(args[1]), runs = Integer.parseInt(args[2]);
    byte[] bytes = Files.readAllBytes(path);
    List<Event> evs = load(bytes);
    XMLOutputFactory f = new WstxOutputFactory();
    ByteArrayOutputStream sink = new ByteArrayOutputStream(2 * bytes.length);
    write(f, evs, sink);
    int outBytes = sink.size();
    if (args.length > 3) Files.write(Path.of(args[3]), sink.toByteArray());
    long start = System.nanoTime();
    for (int i = 0; i < warmups || System.nanoTime() - start < 1_000_000_000L; i++) write(f, evs, sink);
    StringBuilder samples = new StringBuilder();
    for (int r = 0; r < runs; r++) {
      long t = System.nanoTime();
      int n = 0;
      while (n == 0 || System.nanoTime() - t < 100_000_000L) {
        write(f, evs, sink);
        n++;
      }
      if (r > 0) samples.append(',');
      samples.append("{\"seconds\":").append((System.nanoTime() - t) / 1e9 / n).append(",\"iterations\":").append(n).append('}');
    }
    System.out.println("{\"engine\":\"woodstox\",\"inputBytes\":" + bytes.length + ",\"outputBytes\":" + outBytes
        + ",\"events\":" + evs.size() + ",\"samples\":[" + samples + "]}");
  }
}
