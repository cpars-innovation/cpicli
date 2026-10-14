package cpictl.standin;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertNotNull;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertSame;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

import com.sap.it.script.v2.api.Attachment;
import com.sap.it.script.v2.api.Exchange;
import com.sap.it.script.v2.api.Expression;
import com.sap.it.script.v2.api.Message;
import com.sap.it.script.v2.api.attachment.AttachmentMap;
import com.sap.it.script.v2.api.attachment.DefaultAttachment;
import com.sap.it.script.v2.api.exceptions.HttpOperationFailedException;
import com.sap.it.script.v2.api.exceptions.ScenarioExecutionException;
import com.sap.it.script.v2.api.exceptions.SchemaValidationException;
import com.sap.it.script.v2.api.extensions.ExchangeExtensionFunctions;
import com.sap.it.script.v2.api.extensions.ExtensionFunctions;
import com.sap.it.script.v2.api.language.SimpleBuilder;
import com.sap.it.script.v2.api.language.SimpleExpression;
import com.sap.it.script.v2.api.language.SimpleLanguage;
import java.nio.charset.StandardCharsets;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import javax.activation.DataHandler;
import org.junit.jupiter.api.Test;
import org.xml.sax.SAXParseException;

/** Conformance of the version 2 API: message, exchange, attachments, Simple language, exceptions. */
class MessageV2ConformanceTest {
    @Test
    void messageBodyHeadersProperties() {
        MessageV2 m = new MessageV2();
        m.setBody(new Bodies.CachedStream("<a/>".getBytes(StandardCharsets.UTF_8)));
        assertEquals("<a/>", m.getBody(String.class));
        m.resetBodyStream();
        assertEquals(4, m.getBodySize());
        m.setHeader("Content-Type", "text/xml");
        assertEquals("text/xml", m.getHeader("content-type"));
        assertEquals("dflt", m.getHeader("missing", "dflt"));
        assertEquals("text/xml", m.getHeader("CONTENT-TYPE", String.class));
        m.setHeaders(Map.of("A", "1"));
        assertEquals(1, m.getHeaders().size());
        m.setProperty("P", 2);
        assertEquals(2, m.getProperty("P"));
        m.setProperties(Map.of("Q", 3));
        assertNull(m.getProperty("P"));
        m.clearSoapHeaders();
    }

    @Test
    void attachments() {
        Message m = new MessageV2();
        Attachment a = new DefaultAttachment(new DataHandler("hello", "text/plain"));
        m.addAttachmentObject("a", a);
        m.addAttachmentHeader("X", "1", a);
        m.addAttachmentHeader("X", "2", a);
        assertEquals("1,2", m.getAttachmentHeader("X", a));
        assertEquals(List.of("1", "2"), a.getHeaderAsList("x"));
        m.setAttachmentHeader("X", "3", a);
        assertEquals("3", a.getHeader("X"));
        m.removeAttachmentHeader("X", a);
        assertTrue(a.getHeaderNames().isEmpty());
        a.setHeader("Y", "y");
        a.clearHeaders();
        assertEquals(5, m.getAttachmentsSize());
        assertSame(a, m.getAttachmentObjects().get("a"));
        Map<String, DataHandler> handlers = m.getAttachments();
        assertEquals(1, handlers.size());
        handlers.put("b", new DataHandler("bb", "text/plain"));
        assertEquals(2, m.getAttachmentObjects().size());
        assertTrue(handlers instanceof AttachmentMap am && am.getOriginalMap() == m.getAttachmentObjects());
        m.setAttachments(Map.of("c", new DataHandler("c", "text/plain")));
        assertEquals(1, m.getAttachmentObjects().size());
        m.setAttachmentObjects(new HashMap<>(Map.of("d", a)));
        m.clearAttachments();
        assertEquals(0, m.getAttachmentsSize());
        assertEquals(new DefaultAttachment(a.getDataHandler()).hashCode(), new DefaultAttachment(a.getDataHandler()).hashCode());
    }

    @Test
    void exchangeAndExtensionFunctions() {
        MessageV2 m = new MessageV2();
        Exchange ex = m.exchange();
        ex.setProperty("P", "v");
        assertEquals("v", m.getProperty("P"));
        assertEquals("v", ex.getProperty("P"));
        assertEquals("d", ex.getProperty("none", "d"));
        assertTrue(ex.hasProperties());
        assertEquals(ex.getProperties(), ex.getAllProperties());
        assertEquals("v", ex.removeProperty("P"));
        assertFalse(ex.hasProperties());
        assertSame(m, ex.getIn());
        assertSame(m, ex.getOut());
        assertSame(m, ex.getMessage());
        ex.setException(new IllegalStateException("x"));
        assertEquals("x", ex.getException().getMessage());
        assertTrue(ex.getExchangeId().startsWith("ID-"));
        assertEquals("cpictl-test", ex.getContext().getName());
        assertEquals("3.14.10", ex.getContext().getVersion());
        assertTrue(ex.getContext().getUptimeMillis() >= 0);
        assertNotNull(ex.getContext().resolveLanguage("simple"));
        assertThrows(UnsupportedInStandInsException.class, () -> ex.getContext().resolveLanguage("groovy"));
        assertEquals("CamelFileName", Exchange.FILE_NAME);
        assertEquals("Content-Type", Exchange.CONTENT_TYPE);

        ExchangeExtensionFunctions f = (ExchangeExtensionFunctions) m.getExtensionFunctions(ExchangeExtensionFunctions.class);
        assertTrue(f.getCreated() > 0);
        assertEquals(f.getCreated(), f.getExchangeCreated());
        assertThrows(UnsupportedInStandInsException.class, () -> m.getExtensionFunctions(ExtensionFunctions.class));
    }

    @Test
    void simpleLanguage() {
        MessageV2 m = new MessageV2();
        m.setBody("B");
        m.setHeader("OrderNo", "4711");
        m.setProperty("Mode", "test");
        Exchange ex = m.exchange();
        assertEquals("B-4711-test", SimpleBuilder.simple("${body}-${header.OrderNo}-${exchangeProperty.Mode}").evaluate(ex, String.class));
        assertEquals("4711", SimpleBuilder.simple("${in.header[OrderNo]}").evaluate(ex, String.class));
        assertEquals(4711, SimpleBuilder.simple("${header.OrderNo}", Integer.class).evaluate(ex, Integer.class));
        assertEquals("test", new SimpleExpression("${property.Mode}").evaluate(ex, String.class));
        Expression e = new SimpleLanguage().createExpression("${exchangeId}");
        assertEquals(ex.getExchangeId(), e.evaluate(ex, String.class));
        assertTrue(SimpleBuilder.simple("${header.OrderNo} == '4711'").matches(ex));
        assertFalse(SimpleBuilder.simple("${header.OrderNo} != '4711'").matches(ex));
        assertEquals(4, SimpleBuilder.simple("${date:now:yyyy}").evaluate(ex, String.class).length());
        SimpleBuilder b = SimpleBuilder.simpleF("${header.%s}", "OrderNo");
        b.init(ex.getContext());
        b.setResultType(String.class);
        assertEquals(String.class, b.getResultType());
        assertEquals("${header.OrderNo}", b.getText());
        assertEquals(b.getText(), b.getExpressionText());
        assertTrue(b.toString().contains("Simple"));
        assertEquals("4711", SimpleBuilder.simpleF("${header.%s}", String.class, "OrderNo").evaluate(ex, String.class));
        assertThrows(UnsupportedInStandInsException.class, () -> SimpleBuilder.simple("${bean:x}").evaluate(ex, String.class));
    }

    @Test
    void exceptions() {
        HttpOperationFailedException h = new HttpOperationFailedException("https://user:pw@host/x", 302, "Found", "/y",
                Map.of("A", "b"), "body");
        assertFalse(h.getMessage().contains("pw"), "credentials never appear in the message");
        assertTrue(h.isRedirectError());
        assertTrue(h.hasRedirectLocation());
        assertEquals("/y", h.getRedirectLocation());
        assertEquals(302, h.getStatusCode());
        assertEquals("Found", h.getStatusText());
        assertEquals("b", h.getResponseHeaders().get("A"));
        assertEquals("body", h.getResponseBody());
        assertEquals("https://user:pw@host/x", h.getUrl());
        assertEquals("https://host/x", HttpOperationFailedException.printableUri("https://user:pw@host/x"));

        ScenarioExecutionException s = new ScenarioExecutionException("m", new RuntimeException());
        s.setExceptionType("T");
        assertEquals("T", s.getExceptionType());
        assertNull(new ScenarioExecutionException().getMessage());
        assertEquals("m", new ScenarioExecutionException("m").getMessage());
        assertNotNull(new ScenarioExecutionException(new RuntimeException("c")).getCause());

        SAXParseException err = new SAXParseException("bad", null);
        SchemaValidationException v = new SchemaValidationException("order.xsd", List.of(), List.of(err), List.of());
        assertTrue(v.getMessage().contains("bad"));
        assertEquals("order.xsd", v.getSchema());
        assertEquals(1, v.getErrors().size());
        assertEquals(0, v.getFatalErrors().size());
        assertEquals(0, v.getWarnings().size());
    }
}
