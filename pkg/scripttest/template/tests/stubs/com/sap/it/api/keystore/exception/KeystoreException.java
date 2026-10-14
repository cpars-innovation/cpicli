package com.sap.it.api.keystore.exception;

/** Stand-in: a keystore entry is missing or unreadable. */
public class KeystoreException extends com.sap.it.api.exception.ITApiException {
    private static final long serialVersionUID = 1L;

    public KeystoreException() {
        super();
    }

    public KeystoreException(String message) {
        super(message);
    }

    public KeystoreException(String message, Exception cause) {
        super(message, cause);
    }

    public KeystoreException(Exception cause) {
        super(cause);
    }
}
