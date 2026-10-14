package com.sap.it.api.asdk.runtime;

import java.util.Map;
import cpictl.standin.Services;

/** Stand-in: creates SDK services, e.g. {@code new Factory(DataStoreService.class).getService(new HashMap())}. */
public class Factory {
    private final Class<?> serviceClass;

    public Factory(Class<?> serviceClass) {
        this.serviceClass = serviceClass;
    }

    @SuppressWarnings("unchecked")
    public <T> T getService(Map<String, Object> context) {
        return (T) Services.get(serviceClass);
    }

    public <T> T getService() {
        return getService(null);
    }
}
