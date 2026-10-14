package cpictl.standin;

import java.io.ByteArrayInputStream;
import java.io.IOException;
import java.io.InputStream;
import java.io.Reader;
import java.io.StringReader;
import java.io.StringWriter;
import java.io.UncheckedIOException;
import java.math.BigDecimal;
import java.math.BigInteger;
import java.nio.ByteBuffer;
import java.nio.charset.StandardCharsets;
import javax.xml.parsers.DocumentBuilderFactory;
import javax.xml.transform.OutputKeys;
import javax.xml.transform.Source;
import javax.xml.transform.Transformer;
import javax.xml.transform.TransformerFactory;
import javax.xml.transform.dom.DOMSource;
import javax.xml.transform.stream.StreamResult;
import javax.xml.transform.stream.StreamSource;
import org.w3c.dom.Document;
import org.w3c.dom.Element;
import org.w3c.dom.Node;

/**
 * Body and header conversions as Camel's type converters do them on the runtime for the types scripts
 * ask for: String, byte[], InputStream, Reader, DOM Document / Element / Node, XML Source, numbers and
 * booleans. A conversion outside this list throws {@link UnsupportedInStandInsException}.
 */
public final class Bodies {
    private Bodies() {}

    /** Re-readable stream like CPI's stream cache: each read of the body starts at the beginning. */
    public static final class CachedStream extends ByteArrayInputStream {
        public CachedStream(byte[] data) {
            super(data);
        }

        public byte[] bytes() {
            return java.util.Arrays.copyOf(buf, count);
        }
    }

    @SuppressWarnings("unchecked")
    public static <T> T convert(Object value, Class<T> type) {
        if (value == null || type == null) {
            return null;
        }
        if (type == Object.class) {
            return (T) value;
        }
        if (type.isInstance(value) && !(value instanceof InputStream) && !(value instanceof Reader)) {
            return (T) value;
        }
        if (type == String.class || type == CharSequence.class) {
            return (T) asString(value);
        }
        if (type == byte[].class) {
            return (T) asBytes(value);
        }
        if (type == InputStream.class || type == ByteArrayInputStream.class) {
            return (T) new ByteArrayInputStream(asBytes(value));
        }
        if (type == Reader.class) {
            return (T) new StringReader(asString(value));
        }
        if (type == char[].class) {
            return (T) asString(value).toCharArray();
        }
        if (type == ByteBuffer.class) {
            return (T) ByteBuffer.wrap(asBytes(value));
        }
        if (type == Document.class || type == Node.class || type == Element.class) {
            Document doc = value instanceof Document d ? d : parse(asBytes(value));
            return (T) (type == Element.class ? doc.getDocumentElement() : doc);
        }
        if (type == Source.class || type == StreamSource.class) {
            return (T) new StreamSource(new ByteArrayInputStream(asBytes(value)));
        }
        if (type == DOMSource.class) {
            return (T) new DOMSource(convert(value, Document.class));
        }
        String s = asString(value).trim();
        try {
            if (type == Integer.class || type == int.class) return (T) Integer.valueOf(s);
            if (type == Long.class || type == long.class) return (T) Long.valueOf(s);
            if (type == Double.class || type == double.class) return (T) Double.valueOf(s);
            if (type == Float.class || type == float.class) return (T) Float.valueOf(s);
            if (type == Short.class || type == short.class) return (T) Short.valueOf(s);
            if (type == BigDecimal.class) return (T) new BigDecimal(s);
            if (type == BigInteger.class) return (T) new BigInteger(s);
            if (type == Boolean.class || type == boolean.class) return (T) Boolean.valueOf(s);
        } catch (NumberFormatException e) {
            return null; // Camel's converters answer null when the value does not convert
        }
        throw new UnsupportedInStandInsException("Converting " + value.getClass().getName() + " to " + type.getName());
    }

    public static String asString(Object value) {
        if (value == null) {
            return null;
        }
        if (value instanceof String s) {
            return s;
        }
        if (value instanceof byte[] || value instanceof InputStream || value instanceof ByteBuffer) {
            return new String(asBytes(value), StandardCharsets.UTF_8);
        }
        if (value instanceof Reader r) {
            return read(r);
        }
        if (value instanceof char[] c) {
            return new String(c);
        }
        if (value instanceof Node n) {
            return serialize(new DOMSource(n));
        }
        if (value instanceof Source src) {
            return serialize(src);
        }
        return value.toString();
    }

    public static byte[] asBytes(Object value) {
        if (value == null) {
            return null;
        }
        if (value instanceof byte[] b) {
            return b;
        }
        if (value instanceof CachedStream c) {
            return c.bytes();
        }
        if (value instanceof InputStream in) {
            try {
                if (in.markSupported()) {
                    in.reset(); // re-readable like the stream cache
                }
                byte[] data = in.readAllBytes();
                if (in.markSupported()) {
                    in.reset();
                }
                return data;
            } catch (IOException e) {
                throw new UncheckedIOException(e);
            }
        }
        if (value instanceof ByteBuffer bb) {
            ByteBuffer d = bb.duplicate();
            byte[] out = new byte[d.remaining()];
            d.get(out);
            return out;
        }
        return asString(value).getBytes(StandardCharsets.UTF_8);
    }

    /**
     * The bytes of an attachment. Never {@code DataHandler.getInputStream()}: for a handler over an
     * object without a content handler it blocks forever on a pipe whose writer thread failed.
     */
    public static byte[] attachmentBytes(javax.activation.DataHandler dh) {
        try {
            Object c = dh.getContent();
            if (c instanceof String || c instanceof byte[] || c instanceof InputStream) {
                return asBytes(c);
            }
            java.io.ByteArrayOutputStream out = new java.io.ByteArrayOutputStream();
            dh.writeTo(out);
            return out.toByteArray();
        } catch (IOException e) {
            throw new UncheckedIOException(e);
        }
    }

    static String read(Reader r) {
        try {
            StringWriter w = new StringWriter();
            r.transferTo(w);
            return w.toString();
        } catch (IOException e) {
            throw new UncheckedIOException(e);
        }
    }

    /** Parses XML without DTDs or external entities. */
    public static Document parse(byte[] xml) {
        try {
            DocumentBuilderFactory f = DocumentBuilderFactory.newInstance();
            f.setNamespaceAware(true);
            f.setFeature("http://apache.org/xml/features/disallow-doctype-decl", true);
            f.setXIncludeAware(false);
            f.setExpandEntityReferences(false);
            javax.xml.parsers.DocumentBuilder b = f.newDocumentBuilder();
            b.setErrorHandler(new org.xml.sax.helpers.DefaultHandler() {
                @Override
                public void fatalError(org.xml.sax.SAXParseException e) throws org.xml.sax.SAXException {
                    throw e; // no "[Fatal Error]" lines on stderr
                }
            });
            return b.parse(new ByteArrayInputStream(xml));
        } catch (Exception e) {
            throw new IllegalArgumentException("the body is not XML: " + e.getMessage(), e);
        }
    }

    static String serialize(Source src) {
        try {
            Transformer t = TransformerFactory.newInstance().newTransformer();
            if (src instanceof DOMSource ds && !(ds.getNode() instanceof Document)) {
                t.setOutputProperty(OutputKeys.OMIT_XML_DECLARATION, "yes");
            }
            StringWriter w = new StringWriter();
            t.transform(src, new StreamResult(w));
            return w.toString();
        } catch (Exception e) {
            throw new IllegalStateException("serializing XML: " + e.getMessage(), e);
        }
    }
}
