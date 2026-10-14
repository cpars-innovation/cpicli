package com.sap.it.api.securestore.exception;

/** Stand-in: a secure store read failed. */
public class SecureStoreException extends com.sap.it.api.exception.ITApiException {
    private static final long serialVersionUID = 1L;

    public SecureStoreException() {
        super();
    }

    public SecureStoreException(String message) {
        super(message);
    }

    public SecureStoreException(String message, Exception cause) {
        super(message, cause);
    }

    public SecureStoreException(Exception cause) {
        super(cause);
    }
}
