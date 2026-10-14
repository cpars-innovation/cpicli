package com.sap.it.api;

import com.sap.it.api.exception.InvalidContextException;
import com.sap.it.api.impl.ITApiFactoryRegistry;
import com.sap.it.spi.ITApiHandler;
import cpictl.standin.Services;

/**
 * Stand-in: returns SAP's services (secure store, keystore, Partner Directory, value mappings, number
 * ranges). The stand-in services answer from the test case, never from a tenant.
 */
public class ITApiFactory {
    public ITApiFactory() {}

    /** @deprecated use {@link #getService(Class, Object)} */
    @Deprecated
    public static <T extends ITApi> T getApi(Class<T> api, Object context) throws InvalidContextException {
        return getService(api, context);
    }

    public static <T extends ITApi> T getService(Class<T> api, Object context) throws InvalidContextException {
        ITApiHandler<?> handler = ITApiFactoryRegistry.getApiHandler(api);
        if (handler != null) {
            return api.cast(handler.getApi(context));
        }
        return Services.get(api);
    }
}
