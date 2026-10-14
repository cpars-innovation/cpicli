package com.sap.it.api.asdk.datastore;

import java.util.HashMap;
import java.util.Map;

/** Stand-in: an entry of a data store. */
public class DataBean {
    private byte[] data;
    private Map<String, Object> headers = new HashMap<>();
    private MetaData metaData;

    public DataBean() {}

    public byte[] getDataAsArray() {
        return data;
    }

    public void setDataAsArray(byte[] data) {
        this.data = data;
    }

    public Map<String, Object> getHeaders() {
        return headers;
    }

    public void setHeaders(Map<String, Object> headers) {
        this.headers = headers == null ? new HashMap<>() : headers;
    }

    public MetaData getMetaData() {
        return metaData;
    }

    public void setMetaData(MetaData metaData) {
        this.metaData = metaData;
    }
}
