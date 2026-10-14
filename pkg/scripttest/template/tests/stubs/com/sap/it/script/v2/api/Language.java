package com.sap.it.script.v2.api;

/** Stand-in: an expression language. */
public interface Language {
    Expression createExpression(String expression);
}
