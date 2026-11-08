import com.ctc.wstx.stax.WstxInputFactory;
import java.io.ByteArrayInputStream;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.Locale;
import javax.xml.stream.XMLInputFactory;
import javax.xml.stream.XMLStreamConstants;
import javax.xml.stream.XMLStreamException;
import javax.xml.stream.XMLStreamReader;
import org.codehaus.stax2.XMLInputFactory2;

public final class ReaderBench {
  record Result(long events, int checksum) {}

  private static int fold(int seed, CharSequence value, int start, int end) {
    for (int i = start; i < end; i++) seed = seed * 31 + value.charAt(i);
    return seed;
  }

  private static int fold(int seed, String value) {
    return fold(seed, value, 0, value.length());
  }

  private static boolean xmlSpace(char value) {
    return value == ' ' || value == '\t' || value == '\r' || value == '\n';
  }

  private static final class Checksum {
    long events;
    int value;
    final StringBuilder text = new StringBuilder();

    void flushText() {
      int start = 0;
      int end = text.length();
      while (start < end && xmlSpace(text.charAt(start))) start++;
      while (end > start && xmlSpace(text.charAt(end - 1))) end--;
      if (end > start) {
        events++;
        value = fold(fold(value, "T"), text, start, end);
      }
      text.setLength(0);
    }
  }

  private static XMLInputFactory factory(String engine) {
    // newDefaultFactory bypasses service discovery even with Woodstox on the classpath.
    XMLInputFactory factory = engine.equals("woodstox")
        ? new WstxInputFactory() : XMLInputFactory.newDefaultFactory();
    factory.setProperty(XMLInputFactory.SUPPORT_DTD, false);
    factory.setProperty(XMLInputFactory.IS_SUPPORTING_EXTERNAL_ENTITIES, false);
    factory.setProperty(XMLInputFactory.IS_REPLACING_ENTITY_REFERENCES, true);
    factory.setXMLResolver((publicId, systemId, baseUri, namespace) -> {
      throw new XMLStreamException("External entities are outside this benchmark contract");
    });
    return factory;
  }

  private static Result consume(String engine, Path path, byte[] memory) throws Exception {
    byte[] bytes = memory == null ? Files.readAllBytes(path) : memory;
    XMLStreamReader reader = factory(engine).createXMLStreamReader(new ByteArrayInputStream(bytes));
    Checksum checksum = new Checksum();
    try {
      while (reader.hasNext()) {
        int type = reader.next();
        if (type == XMLStreamConstants.START_ELEMENT) {
          checksum.flushText();
          checksum.events++;
          checksum.value = fold(fold(checksum.value, "S"), reader.getName().toString());
          int count = reader.getAttributeCount();
          checksum.value = (checksum.value ^ count) * 16777619;
          for (int i = 0; i < count; i++) {
            checksum.value = fold(checksum.value, reader.getAttributeName(i).toString());
            checksum.value = fold(checksum.value, reader.getAttributeValue(i));
          }
        } else if (type == XMLStreamConstants.END_ELEMENT) {
          checksum.flushText();
          checksum.events++;
          checksum.value = fold(fold(checksum.value, "E"), reader.getName().toString());
        } else if (type == XMLStreamConstants.CHARACTERS || type == XMLStreamConstants.CDATA
            || type == XMLStreamConstants.SPACE) {
          checksum.text.append(reader.getText());
        } else if (type == XMLStreamConstants.DTD || type == XMLStreamConstants.ENTITY_REFERENCE) {
          throw new XMLStreamException("DTD/unexpanded entities are outside this benchmark contract");
        }
      }
      checksum.flushText();
      return new Result(checksum.events, checksum.value);
    } finally {
      reader.close();
    }
  }

  private static Result check(Result expected, Result actual) {
    if (expected != null && !expected.equals(actual)) {
      throw new IllegalStateException("Document checksum mismatch: " + expected + " != " + actual);
    }
    return expected == null ? actual : expected;
  }

  public static void main(String[] args) throws Exception {
    if (args.length != 5) throw new IllegalArgumentException("ENGINE FILE WARMUPS RUNS MODE");
    String engine = args[0];
    if (!engine.equals("woodstox") && !engine.equals("jdk")) {
      throw new IllegalArgumentException("ENGINE must be woodstox or jdk");
    }
    Path path = Path.of(args[1]);
    int warmups = Integer.parseInt(args[2]);
    int runs = Integer.parseInt(args[3]);
    String mode = args[4];
    if (warmups < 0 || runs < 1 || (!mode.equals("file") && !mode.equals("memory"))) {
      throw new IllegalArgumentException("Require WARMUPS >= 0, RUNS >= 1, MODE file or memory");
    }
    byte[] memory = mode.equals("memory") ? Files.readAllBytes(path) : null;
    String version = engine.equals("woodstox")
        ? WstxInputFactory.class.getPackage().getImplementationVersion() + "; stax2-api "
            + XMLInputFactory2.class.getPackage().getImplementationVersion() + "; Java "
            + System.getProperty("java.version")
        : System.getProperty("java.version");
    Result expected = null;
    long warmupIterations = 0;
    long warmupElapsed = 0;
    if (warmups > 0) {
      long started = System.nanoTime();
      do {
        expected = check(expected, consume(engine, path, memory));
        warmupIterations++;
        warmupElapsed = System.nanoTime() - started;
      } while (warmupIterations < warmups || warmupElapsed < 1_000_000_000L);
    }
    StringBuilder json = new StringBuilder("{\"engine\":\"").append(engine)
        .append("\",\"version\":\"").append(version).append("\",\"warmupIterations\":")
        .append(warmupIterations).append(",\"warmupSeconds\":")
        .append(warmupElapsed / 1_000_000_000.0).append(",\"samples\":[");
    for (int i = 0; i < runs; i++) {
      long iterations = 0;
      long elapsed;
      long started = System.nanoTime();
      do {
        expected = check(expected, consume(engine, path, memory));
        iterations++;
        elapsed = System.nanoTime() - started;
      } while (elapsed < 100_000_000L);
      if (i > 0) json.append(',');
      json.append(String.format(Locale.ROOT,
          "{\"seconds\":%.12f,\"iterations\":%d,\"events\":%d,\"checksum\":%d}",
          elapsed / 1_000_000_000.0 / iterations, iterations, expected.events(), expected.checksum()));
    }
    System.out.println(json.append("]}"));
  }
}
