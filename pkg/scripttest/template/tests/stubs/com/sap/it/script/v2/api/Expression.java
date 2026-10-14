package com.sap.it.script.v2.api;

/** Stand-in: an expression evaluated against an exchange. */
public interface Expression {
    <T> T evaluate(Exchange exchange, Class<T> type);
}
