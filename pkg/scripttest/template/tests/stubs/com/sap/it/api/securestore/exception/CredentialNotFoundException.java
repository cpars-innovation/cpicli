package com.sap.it.api.securestore.exception;

/** Stand-in: no credential with the alias. */
public class CredentialNotFoundException extends SecureStoreException {
    private static final long serialVersionUID = 1L;

    public CredentialNotFoundException() {
        super();
    }

    public CredentialNotFoundException(String message) {
        super(message);
    }

    public CredentialNotFoundException(String message, Exception cause) {
        super(message, cause);
    }

    public CredentialNotFoundException(Exception cause) {
        super(cause);
    }
}
