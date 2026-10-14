package com.sap.it.api.exception;

/** Stand-in: an error of an SAP script API service. */
public class ITApiException extends Exception {
    private static final long serialVersionUID = 1L;

    public ITApiException() {
        super();
    }

    public ITApiException(String message) {
        super(message);
    }

    public ITApiException(String message, Exception cause) {
        super(message, cause);
    }

    public ITApiException(Exception cause) {
        super(cause);
    }
}
