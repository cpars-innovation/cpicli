package com.sap.it.api.exception;

/** Stand-in: the context passed to ITApiFactory is not valid. */
public class InvalidContextException extends ITApiException {
    private static final long serialVersionUID = 1L;

    public InvalidContextException() {
        super();
    }

    public InvalidContextException(String message) {
        super(message);
    }
}
