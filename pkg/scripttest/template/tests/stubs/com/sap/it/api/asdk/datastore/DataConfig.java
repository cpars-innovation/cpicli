package com.sap.it.api.asdk.datastore;

/** Stand-in: where and how {@link DataStoreService#put} writes an entry. */
public class DataConfig {
    private String storeName;
    private String qualifier;
    private String id;
    private boolean overwrite;
    private long expires;
    private long retentionThreshold;

    public DataConfig() {}

    public String getStoreName() {
        return storeName;
    }

    public void setStoreName(String storeName) {
        this.storeName = storeName;
    }

    public String getQualifier() {
        return qualifier;
    }

    public void setQualifier(String qualifier) {
        this.qualifier = qualifier;
    }

    public String getId() {
        return id;
    }

    public void setId(String id) {
        this.id = id;
    }

    public boolean isOverwrite() {
        return overwrite;
    }

    public void setOverwrite(boolean overwrite) {
        this.overwrite = overwrite;
    }

    public long getExpires() {
        return expires;
    }

    public void setExpires(long expires) {
        this.expires = expires;
    }

    public long getRetentionThreshold() {
        return retentionThreshold;
    }

    public void setRetentionThreshold(long retentionThreshold) {
        this.retentionThreshold = retentionThreshold;
    }
}
