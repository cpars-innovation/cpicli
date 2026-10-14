package cpictl.standin;

import com.sap.it.script.v2.api.CamelContext;
import com.sap.it.script.v2.api.Exchange;
import com.sap.it.script.v2.api.Language;
import com.sap.it.script.v2.api.Message;
import com.sap.it.script.v2.api.language.SimpleLanguage;
import java.util.Map;
import java.util.UUID;

/** The version 2 exchange: one message whose properties are the exchange's. */
final class ExchangeV2 implements Exchange {
    private final MessageV2 message;
    private final String id = "ID-cpictl-test-" + UUID.randomUUID();
    private Exception exception;

    ExchangeV2(MessageV2 message) {
        this.message = message;
    }

    @Override
    public Object getProperty(String name) {
        return message.getProperty(name);
    }

    @Override
    @Deprecated
    public Object getProperty(String name, Object defaultValue) {
        Object v = message.getProperty(name);
        return v == null ? defaultValue : v;
    }

    @Override
    public void setProperty(String name, Object value) {
        message.setProperty(name, value);
    }

    @Override
    public Object removeProperty(String name) {
        return message.getProperties().remove(name);
    }

    @Override
    public Map<String, Object> getProperties() {
        return message.getProperties();
    }

    @Override
    public Map<String, Object> getAllProperties() {
        return message.getProperties();
    }

    @Override
    public boolean hasProperties() {
        return !message.getProperties().isEmpty();
    }

    @Override
    public Message getIn() {
        return message;
    }

    @Override
    public Message getOut() {
        return message;
    }

    @Override
    public Message getMessage() {
        return message;
    }

    @Override
    public Exception getException() {
        return exception;
    }

    @Override
    public void setException(Throwable t) {
        exception = t == null ? null : t instanceof Exception e ? e : new RuntimeException(t);
    }

    @Override
    public CamelContext getContext() {
        return CONTEXT;
    }

    @Override
    public String getExchangeId() {
        return id;
    }

    static final CamelContext CONTEXT = new CamelContext() {
        private final long started = System.currentTimeMillis();

        @Override
        public String getName() {
            return "cpictl-test";
        }

        @Override
        public long getUptimeMillis() {
            return System.currentTimeMillis() - started;
        }

        @Override
        public String getVersion() {
            return "3.14.10";
        }

        @Override
        public Language resolveLanguage(String language) {
            if ("simple".equals(language)) {
                return new SimpleLanguage();
            }
            throw new UnsupportedInStandInsException("The language " + language);
        }
    };
}
