package com.sap.it.spi;

import com.sap.it.api.ITApi;

/** Stand-in: provides one service to ITApiFactory. */
public interface ITApiHandler<T extends ITApi> {
    T getApi(Object context);
}
