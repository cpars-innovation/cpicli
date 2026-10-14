package cpictl.standin;

import com.sap.it.api.asdk.datastore.DataBean;
import com.sap.it.api.asdk.datastore.DataConfig;
import com.sap.it.api.asdk.datastore.DataStoreException;
import com.sap.it.api.asdk.datastore.DataStoreService;
import com.sap.it.api.asdk.datastore.MetaData;
import com.sap.it.api.keystore.KeystoreService;
import com.sap.it.api.mapping.MappingContext;
import com.sap.it.api.mapping.Output;
import com.sap.it.api.mapping.ValueMappingApi;
import com.sap.it.api.msglog.MessageLog;
import com.sap.it.api.msglog.MessageLogFactory;
import com.sap.it.api.nrc.NumberRangeConfigurationService;
import com.sap.it.api.nrc.exception.NumberRangeConfigException;
import com.sap.it.api.pd.PartnerDirectoryService;
import com.sap.it.api.securestore.AccessTokenAndUser;
import com.sap.it.api.securestore.SecureStoreService;
import com.sap.it.api.securestore.UserCredential;
import com.sap.it.api.securestore.exception.CredentialNotFoundException;
import java.text.SimpleDateFormat;
import java.util.ArrayList;
import java.util.Collections;
import java.util.Date;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.TimeZone;
import java.util.concurrent.atomic.AtomicLong;

/** The implementations of SAP's services, answering from the running case ({@link CaseContext}). */
public final class Services {
    private Services() {}

    /** The service for an API class; a service the stand-ins lack throws {@link UnsupportedInStandInsException}. */
    public static <T> T get(Class<T> api) {
        Object s;
        if (api == SecureStoreService.class) {
            s = new SecureStore();
        } else if (api == KeystoreService.class) {
            s = new Keystore();
        } else if (api == PartnerDirectoryService.class) {
            s = new PartnerDirectory();
        } else if (api == ValueMappingApi.class) {
            s = new ValueMapping();
        } else if (api == NumberRangeConfigurationService.class) {
            s = new NumberRange();
        } else if (api == DataStoreService.class) {
            s = new DataStore();
        } else {
            throw new UnsupportedInStandInsException("The service " + (api == null ? "null" : api.getName()));
        }
        return api.cast(s);
    }

    /** The binding {@code messageLogFactory}: logs into the running case's log. */
    public static final class LogFactory implements MessageLogFactory {
        @Override
        public MessageLog getMessageLog(Object context) {
            return new Log(CaseContext.current());
        }
    }

    static final class Log implements MessageLog {
        private final CaseContext c;

        Log(CaseContext c) {
            this.c = c;
        }

        private void property(String name, Object value) {
            c.logSection("properties").put(name, value);
        }

        @Override
        public void addAttachmentAsString(String name, String text, String mediaType) {
            Map<String, Object> a = new LinkedHashMap<>();
            a.put("mediaType", mediaType);
            a.put("text", text);
            c.logSection("attachments").put(name, a);
        }

        @Override
        public void addCustomHeaderProperty(String name, String value) {
            c.addCustomHeader(name, value);
        }

        @Override
        public void setBooleanProperty(String name, Boolean value) {
            property(name, value);
        }

        @Override
        public void setDateProperty(String name, Date value) {
            if (value == null) {
                property(name, null);
                return;
            }
            SimpleDateFormat f = new SimpleDateFormat("yyyy-MM-dd'T'HH:mm:ss.SSS'Z'");
            f.setTimeZone(TimeZone.getTimeZone("UTC"));
            property(name, f.format(value));
        }

        @Override
        public void setDoubleProperty(String name, Double value) {
            property(name, value);
        }

        @Override
        public void setFloatProperty(String name, Float value) {
            property(name, value);
        }

        @Override
        public void setIntegerProperty(String name, Integer value) {
            property(name, value);
        }

        @Override
        public void setLongProperty(String name, Long value) {
            property(name, value);
        }

        @Override
        public void setStringProperty(String name, String value) {
            property(name, value);
        }
    }

    static final class SecureStore implements SecureStoreService {
        private Map<String, String> entry(String alias) throws CredentialNotFoundException {
            Map<String, String> e = CaseContext.current().secure.get(alias);
            if (e == null) {
                throw new CredentialNotFoundException("no credential '" + alias + "' in the test case (secure:)");
            }
            return e;
        }

        @Override
        public AccessTokenAndUser getAccesTokenForOauth2AuthorizationCodeCredential(String alias)
                throws CredentialNotFoundException {
            Map<String, String> e = entry(alias);
            String token = e.getOrDefault("value", e.get("password"));
            String user = e.get("user");
            return new AccessTokenAndUser() {
                @Override
                public String getAccessToken() {
                    return token;
                }

                @Override
                public String getUser() {
                    return user;
                }
            };
        }

        @Override
        public UserCredential getUserCredential(String alias) throws CredentialNotFoundException {
            Map<String, String> e = entry(alias);
            String user = e.getOrDefault("user", alias);
            String password = e.containsKey("password") ? e.get("password") : e.get("value");
            return new UserCredential() {
                @Override
                public Map<String, String> getCredentialProperties() {
                    return Map.of();
                }

                @Override
                public char[] getPassword() {
                    return password == null ? null : password.toCharArray();
                }

                @Override
                public String getUsername() {
                    return user;
                }
            };
        }
    }

    static final class ValueMapping implements ValueMappingApi {
        @Override
        public String getMappedValue(String sa, String si, String sv, String ta, String ti) {
            return CaseContext.current().valueMappings.get(String.join("|", sa, si, sv, ta, ti));
        }

        @Override
        public List<String> getMappedValues(String sa, String si, String sv, String ta, String ti) {
            String v = getMappedValue(sa, si, sv, ta, ti);
            return v == null ? List.of() : List.of(v);
        }
    }

    static final class NumberRange implements NumberRangeConfigurationService {
        @Override
        public String getNextValuefromNumberRange(String name, String context) throws NumberRangeConfigException {
            if (name == null || name.isEmpty()) {
                throw new NumberRangeConfigException("number range name is empty");
            }
            return Long.toString(CaseContext.current().numberRanges.computeIfAbsent(name, k -> new AtomicLong()).incrementAndGet());
        }
    }

    static final class DataStore implements DataStoreService {
        private static String key(String store, String qualifier, String id) {
            return store + "\u0000" + (qualifier == null ? "" : qualifier) + "\u0000" + id;
        }

        @Override
        public MetaData put(DataBean data, DataConfig config) throws DataStoreException {
            if (config == null || config.getStoreName() == null) {
                throw new DataStoreException("the data store name is missing");
            }
            String id = config.getId() == null ? java.util.UUID.randomUUID().toString() : config.getId();
            String k = key(config.getStoreName(), config.getQualifier(), id);
            Map<String, Object> store = CaseContext.current().dataStore;
            if (!config.isOverwrite() && store.containsKey(k)) {
                throw new DataStoreException("entry " + id + " exists in data store " + config.getStoreName());
            }
            long now = System.currentTimeMillis();
            MetaData md = new MetaData(config.getStoreName(), config.getQualifier(), id, now,
                    config.getExpires() > 0 ? now + config.getExpires() * 86_400_000L : 0);
            DataBean copy = new DataBean();
            copy.setDataAsArray(data == null ? null : data.getDataAsArray());
            copy.setHeaders(data == null ? null : new LinkedHashMap<>(data.getHeaders()));
            copy.setMetaData(md);
            store.put(k, copy);
            return md;
        }

        @Override
        public DataBean get(String storeName, String id) {
            return get(storeName, null, id);
        }

        @Override
        public DataBean get(String storeName, String qualifier, String id) {
            return (DataBean) CaseContext.current().dataStore.get(key(storeName, qualifier, id));
        }

        @Override
        public List<DataBean> select(String storeName, int numberOfEntries) {
            return select(storeName, null, numberOfEntries);
        }

        @Override
        public List<DataBean> select(String storeName, String qualifier, int numberOfEntries) {
            String prefix = storeName + "\u0000" + (qualifier == null ? "" : qualifier) + "\u0000";
            List<DataBean> out = new ArrayList<>();
            for (Map.Entry<String, Object> e : CaseContext.current().dataStore.entrySet()) {
                if (e.getKey().startsWith(prefix) && out.size() < numberOfEntries) {
                    out.add((DataBean) e.getValue());
                }
            }
            return out;
        }

        @Override
        public boolean delete(String storeName, String id) {
            return delete(storeName, null, id);
        }

        @Override
        public boolean delete(String storeName, String qualifier, String id) {
            return CaseContext.current().dataStore.remove(key(storeName, qualifier, id)) != null;
        }
    }

    /** The context of a message mapping custom function: the case's headers and properties. */
    public static final class Mapping implements MappingContext {
        private final Map<String, Object> headers;
        private final Map<String, Object> properties;

        public Mapping(Map<String, Object> headers, Map<String, Object> properties) {
            this.headers = headers;
            this.properties = properties;
        }

        @Override
        public String getHeader(String name) {
            return Bodies.asString(headers.get(name));
        }

        @Override
        public String getProperty(String name) {
            return Bodies.asString(properties.get(name));
        }

        @Override
        public void setHeader(String name, Object value) {
            headers.put(name, value);
        }

        @Override
        public void setProperty(String name, Object value) {
            properties.put(name, value);
        }
    }

    /** The result queue of an "all values of context" function; SUPPRESS and CONTEXT_CHANGE as markers. */
    public static final class Queue implements Output {
        public static final String SUPPRESS = "__SUPPRESS__";
        public static final String CONTEXT_CHANGE = "__CONTEXT_CHANGE__";
        private final List<Object> values = Collections.synchronizedList(new ArrayList<>());

        @Override
        public void addValue(Object value) {
            values.add(value);
        }

        @Override
        public void addSuppress() {
            values.add(SUPPRESS);
        }

        @Override
        public void addContextChange() {
            values.add(CONTEXT_CHANGE);
        }

        public List<Object> values() {
            return values;
        }
    }
}
