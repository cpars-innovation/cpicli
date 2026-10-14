package com.sap.it.api.impl;

import com.sap.it.api.ITApi;
import com.sap.it.spi.ITApiHandler;
import java.util.Map;
import java.util.concurrent.ConcurrentHashMap;

/**
 * Stand-in: handlers bound for a service class. A bound handler answers before the stand-in
 * services (the key "api" of the properties names the service class).
 */
public class ITApiFactoryRegistry {
    private static final Map<Class<?>, ITApiHandler<?>> HANDLERS = new ConcurrentHashMap<>();

    public ITApiFactoryRegistry() {}

    @SuppressWarnings("rawtypes") // SAP's signature
    public void bindApiFactory(ITApiHandler<?> handler, Map properties) {
        Object api = properties == null ? null : properties.get("api");
        if (api instanceof Class<?> c) {
            HANDLERS.put(c, handler);
        } else if (api instanceof String name) {
            try {
                HANDLERS.put(Class.forName(name), handler);
            } catch (ClassNotFoundException e) {
                throw new IllegalArgumentException("unknown API class " + name, e);
            }
        }
    }

    public void unbindApiFactory(ITApiHandler<?> handler, Map<?, ?> properties) {
        HANDLERS.values().removeIf(h -> h == handler);
    }

    public static ITApiHandler<?> getApiHandler(Class<? extends ITApi> api) {
        return HANDLERS.get(api);
    }
}
