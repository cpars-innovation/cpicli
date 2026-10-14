package cpictl.standin;

import com.sap.gateway.ip.core.customdev.util.SoapHeader;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.TreeMap;

/**
 * Body, headers and properties shared by the v1 and v2 message stand-ins. Headers are case-insensitive
 * like Camel's; properties are case-sensitive.
 */
public abstract class MessageCore {
    protected Object body;
    protected final Map<String, Object> headers = new TreeMap<>(String.CASE_INSENSITIVE_ORDER);
    protected final Map<String, Object> properties = new LinkedHashMap<>();
    protected final List<SoapHeader> soapHeaders = new ArrayList<>();

    public Object getBody() {
        return body;
    }

    public <T> T getBody(Class<T> type) {
        return Bodies.convert(body, type);
    }

    public void setBody(Object exchangeBody) {
        this.body = exchangeBody;
    }

    public long getBodySize() {
        byte[] b = Bodies.asBytes(body);
        return b == null ? 0 : b.length;
    }

    public Map<String, Object> getHeaders() {
        return headers;
    }

    public void setHeaders(Map<String, Object> exchangeHeaders) {
        headers.clear();
        if (exchangeHeaders != null) {
            headers.putAll(exchangeHeaders);
        }
    }

    public void setHeader(String name, Object value) {
        headers.put(name, value);
    }

    public Map<String, Object> getProperties() {
        return properties;
    }

    public void setProperties(Map<String, Object> exchangeProperties) {
        properties.clear();
        if (exchangeProperties != null) {
            properties.putAll(exchangeProperties);
        }
    }

    public void setProperty(String name, Object value) {
        properties.put(name, value);
    }

    public Object getProperty(String name) {
        return properties.get(name);
    }

    public void clearSoapHeaders() {
        soapHeaders.clear();
    }
}
