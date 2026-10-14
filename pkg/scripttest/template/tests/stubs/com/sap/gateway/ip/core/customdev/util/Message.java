package com.sap.gateway.ip.core.customdev.util;

import java.util.HashMap;
import java.util.Map;

public class Message {
    private Object body;
    private Map<String, Object> headers = new HashMap<>();
    private Map<String, Object> properties = new HashMap<>();
    private final MessageLog messageLog = new MessageLog();

    public Object getBody() { return body; }
    @SuppressWarnings("unchecked")
    public <T> T getBody(Class<T> type) {
        if (body == null) return null;
        if (type == String.class) return (T) body.toString();
        return type.cast(body);
    }
    public void setBody(Object body) { this.body = body; }
    public Map<String, Object> getHeaders() { return headers; }
    public void setHeaders(Map<String, Object> headers) { this.headers = new HashMap<>(headers); }
    @SuppressWarnings("unchecked")
    public <T> T getHeader(String name, Class<T> type) {
        Object v = headers.get(name);
        if (v == null) return null;
        if (type == String.class) return (T) v.toString();
        return type.cast(v);
    }
    public void setHeader(String name, Object value) { headers.put(name, value); }
    public Map<String, Object> getProperties() { return properties; }
    public void setProperties(Map<String, Object> properties) { this.properties = new HashMap<>(properties); }
    public Object getProperty(String name) { return properties.get(name); }
    public void setProperty(String name, Object value) { properties.put(name, value); }
    public MessageLog getMessageLog() { return messageLog; }
}
