package com.sap.it.script.v2.api.attachment;

import com.sap.it.script.v2.api.Attachment;
import java.util.ArrayList;
import java.util.Collection;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Objects;
import java.util.TreeMap;
import javax.activation.DataHandler;
import javax.activation.DataSource;

/** Stand-in: an attachment with case-insensitive headers. */
public class DefaultAttachment implements Attachment {
    private final DataHandler dataHandler;
    private final Map<String, List<String>> headers = new TreeMap<>(String.CASE_INSENSITIVE_ORDER);

    public DefaultAttachment(DataHandler dh) {
        this.dataHandler = dh;
    }

    public DefaultAttachment(DataSource ds) {
        this.dataHandler = new DataHandler(ds);
    }

    @Override
    public DataHandler getDataHandler() {
        return dataHandler;
    }

    @Override
    public String getHeader(String name) {
        List<String> v = headers.get(name);
        return v == null || v.isEmpty() ? null : String.join(",", v);
    }

    @Override
    public List<String> getHeaderAsList(String name) {
        List<String> v = headers.get(name);
        return v == null ? null : List.copyOf(v);
    }

    @Override
    public void addHeader(String headerName, String headerValue) {
        headers.computeIfAbsent(headerName, k -> new ArrayList<>()).add(headerValue);
    }

    @Override
    public void setHeader(String headerName, String headerValue) {
        List<String> v = new ArrayList<>();
        v.add(headerValue);
        headers.put(headerName, v);
    }

    @Override
    public void removeHeader(String headerName) {
        headers.remove(headerName);
    }

    @Override
    public Collection<String> getHeaderNames() {
        return new ArrayList<>(new LinkedHashMap<>(headers).keySet());
    }

    @Override
    public void clearHeaders() {
        headers.clear();
    }

    @Override
    public boolean equals(Object other) {
        return other instanceof DefaultAttachment o && Objects.equals(dataHandler, o.dataHandler) && headers.equals(o.headers);
    }

    @Override
    public int hashCode() {
        return Objects.hash(dataHandler, headers);
    }
}
