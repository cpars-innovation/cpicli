package cpictl.standin;

import static org.junit.jupiter.api.Assertions.assertArrayEquals;
import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertInstanceOf;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

import com.sap.gateway.ip.core.customdev.util.AttachmentWrapper;
import com.sap.gateway.ip.core.customdev.util.Message;
import com.sap.gateway.ip.core.customdev.util.SoapHeader;
import java.io.InputStream;
import java.io.Reader;
import java.math.BigDecimal;
import java.nio.ByteBuffer;
import java.nio.charset.StandardCharsets;
import java.util.List;
import java.util.Map;
import javax.activation.DataHandler;
import javax.xml.namespace.QName;
import javax.xml.transform.Source;
import javax.xml.transform.dom.DOMSource;
import org.apache.camel.attachment.DefaultAttachment;
import org.junit.jupiter.api.Test;
import org.w3c.dom.Document;
import org.w3c.dom.Element;
import org.w3c.dom.Node;

/** Conformance of the version 1 message stand-in: body conversions, headers, properties, attachments. */
class MessageConformanceTest {
    private static final String XML = "<Order id=\"1\"><No>4711</No></Order>";

    private static Message message(Object body) {
        Message m = new MessageV1();
        m.setBody(body);
        return m;
    }

    @Test
    void bodyConvertsLikeTheRuntime() throws Exception {
        for (Object body : new Object[] {XML, XML.getBytes(StandardCharsets.UTF_8), new Bodies.CachedStream(XML.getBytes(StandardCharsets.UTF_8))}) {
            Message m = message(body);
            assertEquals(XML, m.getBody(String.class));
            assertArrayEquals(XML.getBytes(StandardCharsets.UTF_8), m.getBody(byte[].class));
            try (InputStream in = m.getBody(InputStream.class)) {
                assertEquals(XML, new String(in.readAllBytes(), StandardCharsets.UTF_8));
            }
            try (Reader r = m.getBody(Reader.class)) {
                assertEquals(XML, Bodies.read(r));
            }
            Document doc = m.getBody(Document.class);
            assertEquals("Order", doc.getDocumentElement().getLocalName());
            assertInstanceOf(Document.class, m.getBody(Node.class));
            assertEquals("1", m.getBody(Element.class).getAttribute("id"));
            assertInstanceOf(Source.class, m.getBody(Source.class));
            assertInstanceOf(DOMSource.class, m.getBody(DOMSource.class));
            assertEquals(XML.length(), m.getBodySize());
            assertEquals(XML, new String(m.getBody(char[].class)));
            assertEquals(XML.length(), m.getBody(ByteBuffer.class).remaining());
        }
        // a stream body stays readable after a script consumed it (stream cache)
        Message m = message(new Bodies.CachedStream("abc".getBytes(StandardCharsets.UTF_8)));
        ((InputStream) m.getBody()).readAllBytes();
        assertEquals("abc", m.getBody(String.class));
        // a DOM body serializes
        Message dom = message(Bodies.parse(XML.getBytes(StandardCharsets.UTF_8)));
        assertTrue(dom.getBody(String.class).contains("<No>4711</No>"));
        assertEquals("", message("").getBody(String.class));
        assertNull(message(null).getBody(String.class));
    }

    @Test
    void numbersBooleansAndUnsupportedConversions() {
        assertEquals(42, message(" 42 ").getBody(Integer.class));
        assertEquals(42L, message("42").getBody(Long.class));
        assertEquals(new BigDecimal("1.5"), message("1.5").getBody(BigDecimal.class));
        assertEquals(Boolean.TRUE, message("true").getBody(Boolean.class));
        assertNull(message("x").getBody(Integer.class), "no conversion: null like Camel");
        assertThrows(UnsupportedInStandInsException.class, () -> message("x").getBody(java.util.UUID.class));
        assertThrows(IllegalArgumentException.class, () -> message("not xml").getBody(Document.class));
    }

    @Test
    void javascriptClassArguments() {
        MessageV1 m = new MessageV1();
        m.setBody("42");
        m.setHeader("N", "7");
        assertEquals("42", m.getBody((Object) String.class));
        assertEquals(7, m.getHeader("N", (Object) Integer.class));
        assertThrows(IllegalArgumentException.class, () -> m.getBody((Object) "not a class"));
    }

    @Test
    void headersAreCaseInsensitiveAndTyped() {
        Message m = new MessageV1();
        m.setHeader("OrderNo", "4711");
        assertEquals("4711", m.getHeaders().get("orderno"));
        assertEquals(4711, m.getHeader("ORDERNO", Integer.class));
        m.setHeaders(Map.of("A", 1));
        assertNull(m.getHeader("OrderNo", String.class));
        assertEquals("1", m.getHeader("a", String.class));
    }

    @Test
    void propertiesAreCaseSensitive() {
        Message m = new MessageV1();
        m.setProperty("Mode", "test");
        assertEquals("test", m.getProperty("Mode"));
        assertNull(m.getProperty("mode"));
        m.setProperties(Map.of("X", 1));
        assertEquals(Map.of("X", 1), m.getProperties());
    }

    @Test
    @SuppressWarnings("deprecation")
    void attachments() throws Exception {
        Message m = new MessageV1();
        AttachmentWrapper a = new AttachmentWrapper(new DataHandler("hello", "text/plain"));
        m.addAttachmentObject("a.txt", a);
        m.addAttachmentHeader("X-Id", "1", a);
        m.setAttachmentHeader("X-Kind", "note", a);
        assertEquals("1", m.getAttachmentHeader("X-Id", a));
        m.removeAttachmentHeader("X-Id", a);
        assertNull(m.getAttachmentHeader("X-Id", a));
        assertEquals("note", a.getHeader("X-Kind"));
        assertEquals(List.of("a.txt"), List.copyOf(m.getAttachments().keySet()));
        assertEquals(5, m.getAttachmentsSize());
        assertEquals(a, m.getAttachmentWrapperObjects().get("a.txt"));

        // deprecated Camel attachments are converted into wrappers with their headers
        DefaultAttachment camel = new DefaultAttachment(new DataHandler("<x/>", "application/xml"));
        camel.addHeader("H", "v");
        m.addAttachmentObject("b.xml", camel);
        assertEquals("v", m.getAttachmentHeader("H", m.getAttachmentObjects().get("b.xml")));
        m.addAttachmentHeader("H2", "w", camel);
        m.setAttachmentHeader("H3", "z", camel);
        assertEquals("w", m.getAttachmentHeader("H2", camel));
        m.removeAttachmentHeader("H2", camel);
        m.setAttachmentObjects(Map.of("c", camel));
        assertEquals(1, m.getAttachmentWrapperObjects().size());

        m.setAttachments(Map.of("d", new DataHandler("dd", "text/plain")));
        assertEquals(2, m.getAttachmentsSize());
        m.setAttachmentWrapperObjects(Map.of());
        assertEquals(0, m.getAttachments().size());
    }

    @Test
    void soapHeaders() throws Exception {
        Message m = new MessageV1();
        SoapHeader h = new SoapHeader(new QName("urn:x", "Trace"), "<t:Trace xmlns:t=\"urn:x\">1</t:Trace>", true, "actor");
        assertEquals("Trace", h.getElement().getLocalName());
        assertTrue(h.isMustUnderstand());
        h.setActor("a2");
        h.setMustUnderstand(false);
        h.setQname(new QName("urn:x", "T2"));
        h.setElement(h.getElement());
        assertEquals("a2", h.getActor());
        assertEquals("T2", h.getQname().getLocalPart());
        m.setSoapHeaders(List.of(h, new SoapHeader(new QName("urn:y", "Y"), h.getElement(), false, null)));
        assertEquals(2, m.getSoapHeaders().size());
        m.clearSoapHeaders();
        assertEquals(0, m.getSoapHeaders().size());
        assertThrows(javax.xml.stream.XMLStreamException.class, () -> new SoapHeader(new QName("a"), "<broken", false, null));
    }
}
