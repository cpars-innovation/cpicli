package com.sap.it.api.nrc.exception;

/** Stand-in: a number range is missing or exhausted. */
public class NumberRangeConfigException extends com.sap.it.api.exception.ITApiException {
    private static final long serialVersionUID = 1L;

    public NumberRangeConfigException(String message) {
        super(message);
    }
}
