package cpictl.standin;

import com.sap.it.script.v2.api.Exchange;
import com.sap.it.script.v2.api.Message;
import java.text.SimpleDateFormat;
import java.util.Date;
import java.util.regex.Matcher;
import java.util.regex.Pattern;

/**
 * The part of Camel's Simple language scripts use: {@code ${body}}, {@code ${in.body}},
 * {@code ${header.X}} / {@code ${in.header.X}} / {@code ${header[X]}}, {@code ${property.X}} /
 * {@code ${exchangeProperty.X}}, {@code ${exchangeId}}, {@code ${camelId}}, {@code ${date:now[:format]}},
 * and as predicate {@code a == b} / {@code a != b}. Anything else throws
 * {@link UnsupportedInStandInsException}.
 */
public final class Simple {
    private Simple() {}

    private static final Pattern TOKEN = Pattern.compile("\\$\\{([^}]*)}");
    private static final Pattern COMPARE = Pattern.compile("^(.*?)\\s+(==|!=)\\s+(.*)$");

    public static Object evaluate(String text, Exchange exchange) {
        Matcher m = TOKEN.matcher(text);
        if (m.matches()) {
            return token(m.group(1).trim(), exchange); // a single token keeps its type
        }
        StringBuilder b = new StringBuilder();
        int last = 0;
        m.reset();
        while (m.find()) {
            b.append(text, last, m.start());
            Object v = token(m.group(1).trim(), exchange);
            b.append(v == null ? "" : Bodies.asString(v));
            last = m.end();
        }
        return b.append(text.substring(last)).toString();
    }

    public static boolean matches(String text, Exchange exchange) {
        Matcher c = COMPARE.matcher(text.trim());
        if (c.matches()) {
            String left = operand(c.group(1).trim(), exchange);
            String right = operand(c.group(3).trim(), exchange);
            boolean eq = left == null ? right == null : left.equals(right);
            return c.group(2).equals("==") == eq;
        }
        Object v = evaluate(text, exchange);
        return v instanceof Boolean bool ? bool : "true".equalsIgnoreCase(Bodies.asString(v));
    }

    private static String operand(String s, Exchange exchange) {
        if (s.length() >= 2 && (s.startsWith("'") && s.endsWith("'") || s.startsWith("\"") && s.endsWith("\""))) {
            return s.substring(1, s.length() - 1);
        }
        if (s.equals("null")) {
            return null;
        }
        Object v = evaluate(s, exchange);
        return v == null ? null : Bodies.asString(v);
    }

    private static Object token(String t, Exchange exchange) {
        Message in = exchange.getIn();
        switch (t) {
            case "body", "in.body", "bodyAs(String)", "bodyAs(java.lang.String)":
                return t.startsWith("bodyAs") ? in.getBody(String.class) : in.getBody();
            case "exchangeId":
                return exchange.getExchangeId();
            case "camelId", "camelContext.name":
                return exchange.getContext().getName();
            case "date:now":
                return new Date();
            default:
                break;
        }
        for (String p : new String[] {"in.header.", "in.headers.", "header.", "headers."}) {
            if (t.startsWith(p)) {
                return in.getHeaders().get(t.substring(p.length()));
            }
        }
        for (String p : new String[] {"header[", "headers[", "in.header[", "in.headers["}) {
            if (t.startsWith(p) && t.endsWith("]")) {
                return in.getHeaders().get(unquote(t.substring(p.length(), t.length() - 1)));
            }
        }
        for (String p : new String[] {"exchangeProperty.", "property."}) {
            if (t.startsWith(p)) {
                return exchange.getProperty(t.substring(p.length()));
            }
        }
        if (t.startsWith("exchangeProperty[") && t.endsWith("]")) {
            return exchange.getProperty(unquote(t.substring("exchangeProperty[".length(), t.length() - 1)));
        }
        if (t.startsWith("date:now:")) {
            return new SimpleDateFormat(t.substring("date:now:".length())).format(new Date());
        }
        throw new UnsupportedInStandInsException("The Simple expression ${" + t + "}");
    }

    private static String unquote(String s) {
        s = s.trim();
        if (s.length() >= 2 && (s.startsWith("'") || s.startsWith("\""))) {
            return s.substring(1, s.length() - 1);
        }
        return s;
    }
}
