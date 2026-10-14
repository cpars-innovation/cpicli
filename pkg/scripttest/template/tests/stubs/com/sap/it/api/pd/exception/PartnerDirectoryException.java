package com.sap.it.api.pd.exception;

/** Stand-in: a Partner Directory read failed. */
public class PartnerDirectoryException extends com.sap.it.api.exception.ITApiException {
    private static final long serialVersionUID = 1L;

    public PartnerDirectoryException() {
        super();
    }

    public PartnerDirectoryException(String message) {
        super(message);
    }

    public PartnerDirectoryException(String message, Exception cause) {
        super(message, cause);
    }

    public PartnerDirectoryException(Exception cause) {
        super(cause);
    }
}
