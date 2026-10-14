package com.sap.it.script.v2.api.exceptions;

/** Stand-in: an error of the integration scenario. */
public class ScenarioExecutionException extends Exception {
    private static final long serialVersionUID = 1L;

    private String exceptionType;

    public ScenarioExecutionException() {
        super();
    }

    public ScenarioExecutionException(String message) {
        super(message);
    }

    public ScenarioExecutionException(Throwable throwable) {
        super(throwable);
    }

    public ScenarioExecutionException(String message, Throwable throwable) {
        super(message, throwable);
    }

    public String getExceptionType() {
        return exceptionType;
    }

    public void setExceptionType(String exceptionType) {
        this.exceptionType = exceptionType;
    }
}
