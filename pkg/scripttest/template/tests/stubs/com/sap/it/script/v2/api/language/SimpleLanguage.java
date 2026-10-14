package com.sap.it.script.v2.api.language;

import com.sap.it.script.v2.api.Expression;
import com.sap.it.script.v2.api.Language;

/** Stand-in: the Simple language. */
public final class SimpleLanguage implements Language {
    public SimpleLanguage() {}

    @Override
    public Expression createExpression(String expression) {
        return new SimpleExpression(expression);
    }
}
