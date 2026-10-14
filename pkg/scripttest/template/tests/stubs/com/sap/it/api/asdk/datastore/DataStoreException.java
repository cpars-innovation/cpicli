package com.sap.it.api.asdk.datastore;

/** Stand-in: a data store operation failed (for example an existing id without overwrite). */
public class DataStoreException extends Exception {
    private static final long serialVersionUID = 1L;

    public DataStoreException(String message) {
        super(message);
    }

    public DataStoreException(String message, Throwable cause) {
        super(message, cause);
    }
}
