package cpictl.standin;

import java.util.Collections;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.atomic.AtomicLong;

/**
 * The answers of one test case for SAP's services (partner directory, value mappings, secure store,
 * keystore) and what the script recorded (message log, data store, number ranges). The runner sets it
 * for the thread that runs the case; services read it through {@link #current()}.
 *
 * <p>Values come from the case only: never a tenant's secrets.
 */
public final class CaseContext {
    private static final InheritableThreadLocal<CaseContext> CURRENT = new InheritableThreadLocal<>();

    /** Partner Directory: "&lt;pid&gt;/&lt;param&gt;" to value (binary values base64). */
    public final Map<String, String> pd;
    /** Value mappings: "srcAgency|srcId|value|tgtAgency|tgtId" to value. */
    public final Map<String, String> valueMappings;
    /** Secure store: alias to {user, password, value}. */
    public final Map<String, Map<String, String>> secure;
    /** Keystore: alias to PEM (certificates, a PKCS#8 private key) or "generate". */
    public final Map<String, String> keystore;

    /** What the script wrote to the message log: properties, customHeaders, attachments. */
    public final Map<String, Object> log = Collections.synchronizedMap(new LinkedHashMap<>());
    /** The data store of this case: "store\u0000qualifier\u0000id" to entry. */
    final Map<String, Object> dataStore = new ConcurrentHashMap<>();
    /** Number ranges of this case: name to last value. */
    final Map<String, AtomicLong> numberRanges = new ConcurrentHashMap<>();
    /** Exchange creation time of this case. */
    public final long created = System.currentTimeMillis();

    public CaseContext(Map<String, String> pd, Map<String, String> valueMappings,
                       Map<String, Map<String, String>> secure, Map<String, String> keystore) {
        this.pd = pd == null ? Map.of() : Map.copyOf(pd);
        this.valueMappings = valueMappings == null ? Map.of() : Map.copyOf(valueMappings);
        this.secure = secure == null ? Map.of() : Map.copyOf(secure);
        this.keystore = keystore == null ? Map.of() : Map.copyOf(keystore);
    }

    /** The context of the running case; an empty one when none is set. */
    public static CaseContext current() {
        CaseContext c = CURRENT.get();
        if (c == null) {
            c = new CaseContext(null, null, null, null);
            CURRENT.set(c);
        }
        return c;
    }

    public static void set(CaseContext c) {
        CURRENT.set(c);
    }

    public static void clear() {
        CURRENT.remove();
    }

    /** Starts a context for a test: {@code CaseContext.builder().pd("PID", "Name", "v").build().install()}. */
    public static Builder builder() {
        return new Builder();
    }

    /** Installs this context for the current thread (and threads it starts); returns it for assertions on {@link #log}. */
    public CaseContext install() {
        set(this);
        return this;
    }

    /** Collects the values a test sets; the keys follow the field documentation above. */
    public static final class Builder {
        private final Map<String, String> pd = new LinkedHashMap<>();
        private final Map<String, String> valueMappings = new LinkedHashMap<>();
        private final Map<String, Map<String, String>> secure = new LinkedHashMap<>();
        private final Map<String, String> keystore = new LinkedHashMap<>();

        /** A Partner Directory string parameter. */
        public Builder pd(String pid, String parameter, String value) {
            pd.put(pid + "/" + parameter, value);
            return this;
        }

        /** A value mapping: source agency, identifier and value to the target value. */
        public Builder valueMapping(String srcAgency, String srcId, String value, String tgtAgency, String tgtId, String result) {
            valueMappings.put(String.join("|", srcAgency, srcId, value, tgtAgency, tgtId), result);
            return this;
        }

        /** A user credential of the secure store (test values only). */
        public Builder secure(String alias, String user, String password) {
            Map<String, String> e = new LinkedHashMap<>();
            e.put("user", user);
            e.put("password", password);
            secure.put(alias, e);
            return this;
        }

        /** A keystore entry: PEM text, or "generate" for a fresh test key with a self-signed certificate. */
        public Builder keystore(String alias, String pemOrGenerate) {
            keystore.put(alias, pemOrGenerate);
            return this;
        }

        public CaseContext build() {
            return new CaseContext(pd, valueMappings, secure, keystore);
        }
    }

    @SuppressWarnings("unchecked")
    <T> Map<String, T> logSection(String name) {
        return (Map<String, T>) log.computeIfAbsent(name, k -> Collections.synchronizedMap(new LinkedHashMap<>()));
    }

    @SuppressWarnings("unchecked")
    void addCustomHeader(String name, String value) {
        Map<String, Object> headers = logSection("customHeaders");
        ((List<String>) headers.computeIfAbsent(name, k -> Collections.synchronizedList(new java.util.ArrayList<>()))).add(value);
    }
}
