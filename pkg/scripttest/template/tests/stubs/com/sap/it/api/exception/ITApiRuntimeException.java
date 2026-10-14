package com.sap.it.api.exception;

/** Stand-in: an unchecked error of an SAP script API service. */
public class ITApiRuntimeException extends RuntimeException {
    private static final long serialVersionUID = 1L;

    public ITApiRuntimeException() {
        super();
    }

    public ITApiRuntimeException(String message) {
        super(message);
    }

    public ITApiRuntimeException(String message, Exception cause) {
        super(message, cause);
    }

    public ITApiRuntimeException(Exception cause) {
        super(cause);
    }
}
