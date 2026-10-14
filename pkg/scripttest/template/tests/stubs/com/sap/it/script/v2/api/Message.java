package com.sap.it.script.v2.api;

import java.util.HashMap;
import java.util.Map;

// Stand-in for the Groovy script step 2.x message (scripts under script/v2/,
// package script.v2). It has only what the 2.x API offers as far as known:
// no getHeader(name) or getHeader(name, Class) (version 1 only), so a test
// fails when a script calls them. Add a method only when the real API has it.
public class Message {
    private Object body;
    private final Map<String, Object> headers = new HashMap<>();
    private final Map<String, Object> properties = new HashMap<>();

    @SuppressWarnings("unchecked")
    public <T> T getBody(Class<T> type) {
        if (body == null) return null;
        if (type == String.class) return (T) body.toString();
        return type.cast(body);
    }
    public void setBody(Object body) { this.body = body; }
    public Map<String, Object> getHeaders() { return headers; }
    public void setHeader(String name, Object value) { headers.put(name, value); }
    public Object getProperty(String name) { return properties.get(name); }
    public void setProperty(String name, Object value) { properties.put(name, value); }
}
