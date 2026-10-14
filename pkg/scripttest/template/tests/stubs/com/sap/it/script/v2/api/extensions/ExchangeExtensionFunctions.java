package com.sap.it.script.v2.api.extensions;

/** Stand-in: exchange timestamps. */
public interface ExchangeExtensionFunctions extends ExtensionFunctions {
    long getCreated();

    long getExchangeCreated();
}
