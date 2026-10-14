package com.sap.it.script.v2.api.language;

import com.sap.it.script.v2.api.Exchange;
import com.sap.it.script.v2.api.Expression;
import cpictl.standin.Bodies;
import cpictl.standin.Simple;

/** Stand-in: a Simple expression (the subset {@link Simple} implements). */
public final class SimpleExpression implements Expression {
    private final String expression;

    public SimpleExpression(String expression) {
        this.expression = expression;
    }

    @Override
    public <T> T evaluate(Exchange exchange, Class<T> type) {
        return Bodies.convert(Simple.evaluate(expression, exchange), type);
    }
}
