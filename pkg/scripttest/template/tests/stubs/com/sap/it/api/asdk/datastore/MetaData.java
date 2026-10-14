package com.sap.it.api.asdk.datastore;

/** Stand-in: the metadata of a data store entry. */
public class MetaData {
    private final String storeName;
    private final String qualifier;
    private final String id;
    private final long createdAt;
    private final long expiresAt;

    public MetaData(String storeName, String qualifier, String id, long createdAt, long expiresAt) {
        this.storeName = storeName;
        this.qualifier = qualifier;
        this.id = id;
        this.createdAt = createdAt;
        this.expiresAt = expiresAt;
    }

    public String getStoreName() {
        return storeName;
    }

    public String getQualifier() {
        return qualifier;
    }

    public String getId() {
        return id;
    }

    public long getCreatedAt() {
        return createdAt;
    }

    public long getExpiresAt() {
        return expiresAt;
    }
}
