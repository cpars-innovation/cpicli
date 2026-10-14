package com.sap.it.script.v2.api.language;

import com.sap.it.script.v2.api.CamelContext;
import com.sap.it.script.v2.api.Exchange;
import com.sap.it.script.v2.api.Expression;
import cpictl.standin.Bodies;
import cpictl.standin.Simple;

/** Stand-in: builds Simple expressions (the subset {@link Simple} implements). */
public final class SimpleBuilder implements Expression {
    private final String text;
    private Class<?> resultType;

    public SimpleBuilder(String text) {
        this.text = text;
    }

    public static SimpleBuilder simple(String text) {
        return new SimpleBuilder(text);
    }

    public static SimpleBuilder simple(String text, Class<?> resultType) {
        return new SimpleBuilder(text).resultType(resultType);
    }

    public static SimpleBuilder simpleF(String formatText, Object... values) {
        return simple(String.format(formatText, values));
    }

    public static SimpleBuilder simpleF(String formatText, Class<?> resultType, Object... values) {
        return simple(String.format(formatText, values), resultType);
    }

    public String getText() {
        return text;
    }

    public String getExpressionText() {
        return text;
    }

    public Class<?> getResultType() {
        return resultType;
    }

    public void setResultType(Class<?> resultType) {
        this.resultType = resultType;
    }

    public SimpleBuilder resultType(Class<?> resultType) {
        this.resultType = resultType;
        return this;
    }

    @Override
    public String toString() {
        return "Simple: " + text;
    }

    @Override
    public <T> T evaluate(Exchange exchange, Class<T> type) {
        Object v = Simple.evaluate(text, exchange);
        if (resultType != null) {
            v = Bodies.convert(v, resultType);
        }
        return Bodies.convert(v, type);
    }

    public boolean matches(Exchange exchange) {
        return Simple.matches(text, exchange);
    }

    public void init(CamelContext context) {
        // nothing to prepare: the subset is interpreted
    }
}
